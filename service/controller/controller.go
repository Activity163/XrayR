package controller

import (
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/stats"

	"github.com/XrayR-project/XrayR/api"
	"github.com/XrayR-project/XrayR/app/mydispatcher"
	"github.com/XrayR-project/XrayR/common/mylego"
	"github.com/XrayR-project/XrayR/common/serverstatus"
	"github.com/XrayR-project/XrayR/internal/snapshot"
	"github.com/XrayR-project/XrayR/observability"
	"github.com/XrayR-project/XrayR/service/diagnostics"
)

type LimitInfo struct {
	end               int64
	currentSpeedLimit int
	originSpeedLimit  uint64
}

// runtimeState is an immutable snapshot of the node/user/tag triple published by
// the node monitor. Readers load the whole snapshot atomically, so they never
// observe a partially updated state and never block the writer. Published values
// are never mutated in place; a new snapshot replaces the previous one.
type runtimeState struct {
	node  *api.NodeInfo
	users *[]api.UserInfo
	tag   string
}

type Controller struct {
	stateMu       sync.RWMutex
	closeOnce     sync.Once
	state         atomic.Pointer[runtimeState]
	server        *core.Instance
	config        *Config
	clientInfo    api.ClientInfo
	apiClient     api.Client
	tasks         []periodicTask
	limitedUsers  map[api.UserInfo]LimitInfo
	warnedUsers   map[api.UserInfo]int
	panelType     string
	capabilities  api.PanelCapabilities
	ibm           inbound.Manager
	obm           outbound.Manager
	stm           stats.Manager
	pm            policy.Manager
	dispatcher    *mydispatcher.DefaultDispatcher
	startAt       time.Time
	logger        *log.Entry
	snapshotStore *snapshot.Store
	lastSync      time.Time
	lastError     string
}

// runtimeSnapshot returns the current node, user list and tag. The returned
// values are immutable and must not be modified by callers.
func (c *Controller) runtimeSnapshot() (node *api.NodeInfo, users *[]api.UserInfo, tag string) {
	if snapshot := c.state.Load(); snapshot != nil {
		return snapshot.node, snapshot.users, snapshot.tag
	}
	return nil, nil, ""
}

// Tag returns the inbound tag currently in use. Safe for concurrent use.
func (c *Controller) Tag() string {
	_, _, tag := c.runtimeSnapshot()
	return tag
}

func (c *Controller) currentNode() *api.NodeInfo {
	node, _, _ := c.runtimeSnapshot()
	return node
}

func (c *Controller) currentUsers() *[]api.UserInfo {
	_, users, _ := c.runtimeSnapshot()
	return users
}

// publishState atomically replaces the node/user/tag triple.
func (c *Controller) publishState(node *api.NodeInfo, users *[]api.UserInfo, tag string) {
	c.state.Store(&runtimeState{node: node, users: users, tag: tag})
}

// publishUsers atomically replaces the user list, keeping node and tag unchanged.
func (c *Controller) publishUsers(users *[]api.UserInfo) {
	node, _, tag := c.runtimeSnapshot()
	c.state.Store(&runtimeState{node: node, users: users, tag: tag})
}

type periodicTask struct {
	tag string
	*task.Periodic
}

// New return a Controller service with default parameters.
func New(server *core.Instance, client api.Client, config *Config, panelType string, capabilities api.PanelCapabilities) *Controller {
	clientInfo := client.Describe()
	logger := log.NewEntry(log.StandardLogger()).WithFields(log.Fields{
		"Host": clientInfo.APIHost,
		"Type": clientInfo.NodeType,
		"ID":   clientInfo.NodeID,
	})
	controller := &Controller{
		server:       server,
		config:       config,
		apiClient:    client,
		panelType:    panelType,
		capabilities: capabilities,
		ibm:          server.GetFeature(inbound.ManagerType()).(inbound.Manager),
		obm:          server.GetFeature(outbound.ManagerType()).(outbound.Manager),
		stm:          server.GetFeature(stats.ManagerType()).(stats.Manager),
		pm:           server.GetFeature(policy.ManagerType()).(policy.Manager),
		dispatcher:   server.GetFeature(mydispatcher.Type()).(*mydispatcher.DefaultDispatcher),
		startAt:      time.Now(),
		logger:       logger,
	}
	if config.SnapshotPath != "" {
		controller.snapshotStore = snapshot.New(config.SnapshotPath, clientInfo, time.Duration(config.SnapshotMaxAge)*time.Second)
	}

	return controller
}

// Start implement the Start() function of the service interface
func (c *Controller) Start() error {
	c.clientInfo = c.apiClient.Describe()
	newNodeInfo, nodeErr := c.apiClient.GetNodeInfo()
	var userInfo *[]api.UserInfo
	var rules *[]api.DetectRule
	if nodeErr == nil {
		userInfo, nodeErr = c.apiClient.GetUserList()
	}
	if nodeErr == nil && !c.config.DisableGetRule {
		if provider, ok := c.apiClient.(api.RuleProvider); ok {
			rules, _ = provider.GetNodeRule()
		}
	}
	if nodeErr != nil && c.snapshotStore != nil {
		var savedAt time.Time
		newNodeInfo, userInfo, rules, savedAt, nodeErr = c.snapshotStore.Load()
		if nodeErr == nil {
			c.logger.WithField("saved_at", savedAt).Warn("Panel is unavailable; using the last valid snapshot")
		}
	}
	if nodeErr != nil {
		return nodeErr
	}
	if err := validateRuntimeSnapshot(newNodeInfo, userInfo); err != nil {
		return err
	}
	nodeTag := c.buildNodeTag(newNodeInfo)
	c.publishState(newNodeInfo, userInfo, nodeTag)

	if err := c.addNewTag(newNodeInfo, nodeTag); err != nil {
		return err
	}
	if err := c.addNewUser(userInfo, newNodeInfo, nodeTag); err != nil {
		_ = c.removeOldTag(nodeTag)
		return err
	}
	if err := c.AddInboundLimiter(nodeTag, newNodeInfo.SpeedLimit, userInfo, c.config.GlobalDeviceLimitConfig); err != nil {
		c.logger.Print(err)
	}
	if rules != nil && len(*rules) > 0 {
		if err := c.UpdateRule(nodeTag, *rules); err != nil {
			c.logger.Print(err)
		}
	}
	if c.snapshotStore != nil {
		if err := c.snapshotStore.Save(newNodeInfo, userInfo, rules); err != nil {
			c.logger.WithError(err).Warn("Failed to save runtime snapshot")
		}
	}

	if c.config.AutoSpeedLimitConfig == nil {
		c.config.AutoSpeedLimitConfig = &AutoSpeedLimitConfig{}
	}
	if c.config.AutoSpeedLimitConfig.Limit > 0 {
		c.limitedUsers = make(map[api.UserInfo]LimitInfo)
		c.warnedUsers = make(map[api.UserInfo]int)
	}
	c.tasks = append(c.tasks,
		periodicTask{tag: "node monitor", Periodic: &task.Periodic{Interval: time.Duration(c.config.UpdatePeriodic) * time.Second, Execute: c.guardTask("node monitor", c.nodeInfoMonitor)}},
		periodicTask{tag: "user monitor", Periodic: &task.Periodic{Interval: time.Duration(c.config.UpdatePeriodic) * time.Second, Execute: c.guardTask("user monitor", c.userInfoMonitor)}},
	)
	if newNodeInfo.EnableTLS && !c.config.EnableREALITY {
		c.tasks = append(c.tasks, periodicTask{tag: "cert monitor", Periodic: &task.Periodic{Interval: time.Duration(c.config.UpdatePeriodic) * time.Minute, Execute: c.guardTask("cert monitor", c.certMonitor)}})
	}
	for i := range c.tasks {
		c.logger.Printf("Start %s periodic task", c.tasks[i].tag)
		go func(periodic *task.Periodic, name string) {
			// task.Periodic stops for good when Execute returns an error, so surface
			// the reason instead of letting the task disappear silently.
			if err := periodic.Start(); err != nil {
				c.logger.WithError(err).Errorf("%s periodic task stopped", name)
			}
		}(c.tasks[i].Periodic, c.tasks[i].tag)
	}
	c.stateMu.Lock()
	c.lastSync = time.Now()
	c.lastError = ""
	c.stateMu.Unlock()
	observability.LastSync.WithLabelValues(c.panelType, newNodeInfo.NodeType).Set(float64(time.Now().Unix()))
	observability.Users.WithLabelValues(c.panelType, newNodeInfo.NodeType).Set(float64(len(*userInfo)))
	return nil
}

// guardTask contains panics raised by a periodic task body.
//
// xray-core's task.Periodic neither recovers from panics nor survives a non-nil
// error (it clears its running flag), so an unguarded panic inside a monitor would
// take down the whole node process. The recovered panic is logged with a stack
// trace and swallowed so the task keeps running on its next tick.
func (c *Controller) guardTask(name string, fn func() error) func() error {
	return func() (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				c.logger.WithFields(log.Fields{
					"task":  name,
					"panic": recovered,
					"stack": string(debug.Stack()),
				}).Error("Recovered from panic in periodic task")
				err = nil
			}
		}()
		return fn()
	}
}

func validateRuntimeSnapshot(node *api.NodeInfo, users *[]api.UserInfo) error {
	if node == nil || node.Port == 0 {
		return errors.New("server port must be greater than zero")
	}
	if users == nil || len(*users) == 0 {
		return errors.New("user list is empty")
	}
	return nil
}

// Close implement the Close() function of the service interface
func (c *Controller) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		for i := range c.tasks {
			if c.tasks[i].Periodic != nil {
				if err := c.tasks[i].Close(); err != nil && closeErr == nil {
					closeErr = fmt.Errorf("%s periodic task close failed: %w", c.tasks[i].tag, err)
				}
			}
		}
		if closer, ok := c.apiClient.(api.Closer); ok {
			if err := closer.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
	})
	return closeErr
}

// DiagnosticStatus returns sanitized, low-cardinality runtime state.
func (c *Controller) DiagnosticStatus() diagnostics.NodeStatus {
	node, users, tag := c.runtimeSnapshot()
	c.stateMu.RLock()
	lastSync, lastError := c.lastSync, c.lastError
	c.stateMu.RUnlock()
	userCount := 0
	if users != nil {
		userCount = len(*users)
	}
	return diagnostics.NodeStatus{
		Panel: c.panelType, NodeID: c.clientInfo.NodeID, NodeType: c.clientInfo.NodeType,
		Ready: node != nil && tag != "", Users: userCount, LastSync: lastSync, LastError: lastError,
	}
}

func (c *Controller) nodeInfoMonitor() (err error) {
	// delay to start
	if time.Since(c.startAt) < time.Duration(c.config.UpdatePeriodic)*time.Second {
		return nil
	}

	// First fetch Node Info
	var nodeInfoChanged = true
	newNodeInfo, err := c.apiClient.GetNodeInfo()
	if err != nil {
		if errors.Is(err, api.ErrNodeNotModified) {
			nodeInfoChanged = false
			newNodeInfo = c.currentNode()
		} else {
			c.logger.Print(err)
			return nil
		}
	}
	if newNodeInfo.Port == 0 {
		return errors.New("server port must > 0")
	}

	// Update User
	var usersChanged = true
	newUserInfo, err := c.apiClient.GetUserList()
	if err != nil {
		if errors.Is(err, api.ErrUserNotModified) {
			usersChanged = false
			newUserInfo = c.currentUsers()
		} else {
			c.logger.Print(err)
			return nil
		}
	}

	// Apply a node change transactionally. Validate the complete replacement
	// before removing the active tag, then rebuild the previous snapshot if any
	// runtime operation fails.
	if nodeInfoChanged {
		activeNode := c.currentNode()
		if !reflect.DeepEqual(activeNode, newNodeInfo) {
			if err := validateRuntimeSnapshot(newNodeInfo, newUserInfo); err != nil {
				c.recordSyncError(err)
				return nil
			}
			newTag := fmt.Sprintf("%s_%s_%d", newNodeInfo.NodeType, c.config.ListenIP, newNodeInfo.Port)
			if newNodeInfo.NodeType != "Shadowsocks-Plugin" {
				if _, err := InboundBuilder(c.config, newNodeInfo, newTag); err != nil {
					c.recordSyncError(err)
					return nil
				}
				if _, err := OutboundBuilder(c.config, newNodeInfo, newTag); err != nil {
					c.recordSyncError(err)
					return nil
				}
			}

			oldNode, oldUsers, oldTag := c.runtimeSnapshot()
			if err := c.removeOldTag(oldTag); err != nil {
				c.recordSyncError(err)
				return nil
			}
			_ = c.DeleteInboundLimiter(oldTag)
			c.publishState(newNodeInfo, newUserInfo, newTag)
			applyErr := c.addNewTag(newNodeInfo, newTag)
			if applyErr == nil {
				applyErr = c.addNewUser(newUserInfo, newNodeInfo, newTag)
			}
			if applyErr == nil {
				applyErr = c.AddInboundLimiter(newTag, newNodeInfo.SpeedLimit, newUserInfo, c.config.GlobalDeviceLimitConfig)
			}
			if applyErr != nil {
				_ = c.removeOldTag(newTag)
				_ = c.DeleteInboundLimiter(newTag)
				c.publishState(oldNode, oldUsers, oldTag)
				rollbackErr := c.addNewTag(oldNode, oldTag)
				if rollbackErr == nil {
					rollbackErr = c.addNewUser(oldUsers, oldNode, oldTag)
				}
				if rollbackErr == nil {
					rollbackErr = c.AddInboundLimiter(oldTag, oldNode.SpeedLimit, oldUsers, c.config.GlobalDeviceLimitConfig)
				}
				if rollbackErr != nil {
					c.recordSyncError(fmt.Errorf("apply failed: %v; rollback failed: %w", applyErr, rollbackErr))
				} else {
					c.recordSyncError(fmt.Errorf("new node configuration rejected and rolled back: %w", applyErr))
				}
				return nil
			}
			c.markSynced()
			nodeInfoChanged = true
		} else {
			nodeInfoChanged = false
		}
	}

	// Check Rule
	if !c.config.DisableGetRule && c.capabilities.Rules {
		if provider, ok := c.apiClient.(api.RuleProvider); ok {
			if ruleList, err := provider.GetNodeRule(); err != nil {
				if !errors.Is(err, api.ErrRuleNotModified) {
					c.logger.Printf("Get rule list failed: %s", err)
				}
			} else if len(*ruleList) > 0 {
				if err := c.UpdateRule(c.Tag(), *ruleList); err != nil {
					c.logger.Print(err)
				}
			}
		}
	}

	if !nodeInfoChanged {
		tag := c.Tag()
		var deleted, added []api.UserInfo
		if usersChanged {
			deleted, added = compareUserList(c.currentUsers(), newUserInfo)
			if len(deleted) > 0 {
				deletedEmail := make([]string, len(deleted))
				for i, u := range deleted {
					deletedEmail[i] = fmt.Sprintf("%s|%s|%d", tag, u.Email, u.UID)
				}
				err := c.removeUsers(deletedEmail, tag)
				if err != nil {
					c.logger.Print(err)
				}
			}
			if len(added) > 0 {
				err = c.addNewUser(&added, c.currentNode(), tag)
				if err != nil {
					c.logger.Print(err)
				}
				// Update Limiter
				if err := c.UpdateInboundLimiter(tag, &added); err != nil {
					c.logger.Print(err)
				}
			}
		}
		c.logger.Printf("%d user deleted, %d user added", len(deleted), len(added))
	}
	c.publishUsers(newUserInfo)
	if c.snapshotStore != nil {
		var rules *[]api.DetectRule
		if !c.config.DisableGetRule {
			if provider, ok := c.apiClient.(api.RuleProvider); ok {
				rules, _ = provider.GetNodeRule()
			}
		}
		node, users, _ := c.runtimeSnapshot()
		if err := c.snapshotStore.Save(node, users, rules); err != nil {
			c.logger.WithError(err).Warn("Failed to update runtime snapshot")
		}
	}
	c.markSynced()
	return nil
}

// markSynced records a successful synchronization and refreshes the gauges.
func (c *Controller) markSynced() {
	node, users, _ := c.runtimeSnapshot()
	c.stateMu.Lock()
	c.lastSync, c.lastError = time.Now(), ""
	c.stateMu.Unlock()
	if node == nil {
		return
	}
	observability.LastSync.WithLabelValues(c.panelType, node.NodeType).Set(float64(time.Now().Unix()))
	if users != nil {
		observability.Users.WithLabelValues(c.panelType, node.NodeType).Set(float64(len(*users)))
	}
}

func (c *Controller) recordSyncError(err error) {
	c.stateMu.Lock()
	c.lastError = err.Error()
	c.stateMu.Unlock()
	c.logger.WithError(err).Warn("Node synchronization failed; keeping the last valid runtime snapshot")
}

func (c *Controller) removeOldTag(oldTag string) (err error) {
	err = c.removeInbound(oldTag)
	if err != nil {
		return err
	}
	err = c.removeOutbound(oldTag)
	if err != nil {
		return err
	}
	return nil
}

func (c *Controller) addNewTag(newNodeInfo *api.NodeInfo, tag string) (err error) {
	if newNodeInfo.NodeType != "Shadowsocks-Plugin" {
		inboundConfig, err := InboundBuilder(c.config, newNodeInfo, tag)
		if err != nil {
			return err
		}
		err = c.addInbound(inboundConfig)
		if err != nil {

			return err
		}
		outBoundConfig, err := OutboundBuilder(c.config, newNodeInfo, tag)
		if err != nil {

			return err
		}
		err = c.addOutbound(outBoundConfig)
		if err != nil {

			return err
		}

	} else {
		return c.addInboundForSSPlugin(*newNodeInfo, tag)
	}
	return nil
}

func (c *Controller) addInboundForSSPlugin(newNodeInfo api.NodeInfo, tag string) (err error) {
	// Shadowsocks-Plugin require a separate inbound for other TransportProtocol likes: ws, grpc
	fakeNodeInfo := newNodeInfo
	fakeNodeInfo.TransportProtocol = "tcp"
	fakeNodeInfo.EnableTLS = false
	// Add a regular Shadowsocks inbound and outbound
	inboundConfig, err := InboundBuilder(c.config, &fakeNodeInfo, tag)
	if err != nil {
		return err
	}
	err = c.addInbound(inboundConfig)
	if err != nil {

		return err
	}
	outBoundConfig, err := OutboundBuilder(c.config, &fakeNodeInfo, tag)
	if err != nil {

		return err
	}
	err = c.addOutbound(outBoundConfig)
	if err != nil {

		return err
	}
	// Add an inbound for upper streaming protocol
	fakeNodeInfo = newNodeInfo
	fakeNodeInfo.Port++
	fakeNodeInfo.NodeType = "dokodemo-door"
	dokodemoTag := fmt.Sprintf("dokodemo-door_%s+1", tag)
	inboundConfig, err = InboundBuilder(c.config, &fakeNodeInfo, dokodemoTag)
	if err != nil {
		return err
	}
	err = c.addInbound(inboundConfig)
	if err != nil {

		return err
	}
	outBoundConfig, err = OutboundBuilder(c.config, &fakeNodeInfo, dokodemoTag)
	if err != nil {

		return err
	}
	err = c.addOutbound(outBoundConfig)
	if err != nil {

		return err
	}
	return nil
}

func (c *Controller) addNewUser(userInfo *[]api.UserInfo, nodeInfo *api.NodeInfo, tag string) (err error) {
	var users []*protocol.User
	switch nodeInfo.NodeType {
	case "V2ray", "Vmess", "Vless":
		if nodeInfo.EnableVless || (nodeInfo.NodeType == "Vless" && nodeInfo.NodeType != "Vmess") {
			users = c.buildVlessUser(userInfo, tag, nodeInfo.VlessFlow)
		} else {
			users = c.buildVmessUser(userInfo, tag)
		}
	case "Trojan":
		users = c.buildTrojanUser(userInfo, tag)
	case "Shadowsocks":
		users = c.buildSSUser(userInfo, nodeInfo.CypherMethod, tag)
	case "Shadowsocks-Plugin":
		users = c.buildSSPluginUser(userInfo, tag)
	default:
		return fmt.Errorf("unsupported node type: %s", nodeInfo.NodeType)
	}

	err = c.addUsers(users, tag)
	if err != nil {
		return err
	}
	c.logger.Printf("Added %d new users", len(*userInfo))
	return nil
}

func compareUserList(old, new *[]api.UserInfo) (deleted, added []api.UserInfo) {
	mSrc := make(map[api.UserInfo]byte) // 按源数组建索引
	mAll := make(map[api.UserInfo]byte) // 源+目所有元素建索引

	var set []api.UserInfo // 交集

	// 1.源数组建立map
	for _, v := range *old {
		mSrc[v] = 0
		mAll[v] = 0
	}
	// 2.目数组中，存不进去，即重复元素，所有存不进去的集合就是并集
	for _, v := range *new {
		before := len(mAll)
		mAll[v] = 1
		if before == len(mAll) { // 长度没变，说明该元素已存在，属于交集
			set = append(set, v)
		}
	}
	// 3.遍历交集，在并集中找，找到就从并集中删，删完后就是补集（即并-交=所有变化的元素）
	for _, v := range set {
		delete(mAll, v)
	}
	// 4.此时，mall是补集，所有元素去源中找，找到就是删除的，找不到的必定能在目数组中找到，即新加的
	for v := range mAll {
		_, exist := mSrc[v]
		if exist {
			deleted = append(deleted, v)
		} else {
			added = append(added, v)
		}
	}

	return deleted, added
}

func limitUser(c *Controller, tag string, user api.UserInfo, silentUsers *[]api.UserInfo) {
	c.limitedUsers[user] = LimitInfo{
		end:               time.Now().Unix() + int64(c.config.AutoSpeedLimitConfig.LimitDuration*60),
		currentSpeedLimit: c.config.AutoSpeedLimitConfig.LimitSpeed,
		originSpeedLimit:  user.SpeedLimit,
	}
	c.logger.Printf("Limit User: %s Speed: %d End: %s", buildUserTag(tag, &user), c.config.AutoSpeedLimitConfig.LimitSpeed, time.Unix(c.limitedUsers[user].end, 0).Format("01-02 15:04:05"))
	user.SpeedLimit = uint64((c.config.AutoSpeedLimitConfig.LimitSpeed * 1000000) / 8)
	*silentUsers = append(*silentUsers, user)
}

func (c *Controller) userInfoMonitor() (err error) {
	if time.Since(c.startAt) < time.Duration(c.config.UpdatePeriodic)*time.Second {
		return nil
	}

	// Read the runtime state once. The monitors share these values with the node
	// monitor, which may replace them at any time.
	tag := c.Tag()
	userList := c.currentUsers()

	if c.capabilities.NodeStatusReport {
		if reporter, ok := c.apiClient.(api.NodeStatusReporter); ok {
			CPU, Mem, Disk, Uptime, statusErr := serverstatus.GetSystemInfo()
			if statusErr != nil {
				c.logger.Print(statusErr)
			} else if statusErr = reporter.ReportNodeStatus(&api.NodeStatus{CPU: CPU, Mem: Mem, Disk: Disk, Uptime: Uptime}); statusErr != nil {
				c.logger.Print(statusErr)
			}
		}
	}

	if c.config.AutoSpeedLimitConfig.Limit > 0 && len(c.limitedUsers) > 0 {
		c.logger.Printf("Limited users:")
		toReleaseUsers := make([]api.UserInfo, 0)
		for user, limitInfo := range c.limitedUsers {
			if time.Now().Unix() > limitInfo.end {
				user.SpeedLimit = limitInfo.originSpeedLimit
				toReleaseUsers = append(toReleaseUsers, user)
				c.logger.Printf("User: %s Speed: %d End: nil (Unlimit)", buildUserTag(tag, &user), user.SpeedLimit)
				delete(c.limitedUsers, user)
			} else {
				c.logger.Printf("User: %s Speed: %d End: %s", buildUserTag(tag, &user), limitInfo.currentSpeedLimit, time.Unix(c.limitedUsers[user].end, 0).Format("01-02 15:04:05"))
			}
		}
		if len(toReleaseUsers) > 0 {
			if err := c.UpdateInboundLimiter(tag, &toReleaseUsers); err != nil {
				c.logger.Print(err)
			}
		}
	}

	var userTraffic []api.UserTraffic
	var upCounterList []stats.Counter
	var downCounterList []stats.Counter
	AutoSpeedLimit := int64(c.config.AutoSpeedLimitConfig.Limit)
	UpdatePeriodic := int64(c.config.UpdatePeriodic)
	limitedUsers := make([]api.UserInfo, 0)
	if userList != nil {
		for _, user := range *userList {
			userTag := buildUserTag(tag, &user)
			up, down, upCounter, downCounter := c.getTraffic(userTag)
			if down > 0 {
				c.logger.Printf("Traffic counted: tag=%s up=%d down=%d", userTag, up, down)
			}
			if up > 0 || down > 0 {
				if AutoSpeedLimit > 0 {
					if down > AutoSpeedLimit*1000000*UpdatePeriodic/8 || up > AutoSpeedLimit*1000000*UpdatePeriodic/8 {
						if _, ok := c.limitedUsers[user]; !ok {
							if c.config.AutoSpeedLimitConfig.WarnTimes == 0 {
								limitUser(c, tag, user, &limitedUsers)
							} else {
								c.warnedUsers[user]++
								if c.warnedUsers[user] > c.config.AutoSpeedLimitConfig.WarnTimes {
									limitUser(c, tag, user, &limitedUsers)
									delete(c.warnedUsers, user)
								}
							}
						}
					} else {
						delete(c.warnedUsers, user)
					}
				}
				userTraffic = append(userTraffic, api.UserTraffic{UID: user.UID, Email: user.Email, Upload: up, Download: down})
				if upCounter != nil {
					upCounterList = append(upCounterList, upCounter)
				}
				if downCounter != nil {
					downCounterList = append(downCounterList, downCounter)
				}
			} else {
				delete(c.warnedUsers, user)
			}
		}
	}
	if len(limitedUsers) > 0 {
		if err := c.UpdateInboundLimiter(tag, &limitedUsers); err != nil {
			c.logger.Print(err)
		}
	}
	if len(userTraffic) > 0 {
		c.logger.Printf("Reporting %d user(s) traffic to panel", len(userTraffic))
		var reportErr error
		if c.capabilities.TrafficReport {
			if reporter, ok := c.apiClient.(api.TrafficReporter); ok {
				if !c.config.DisableUploadTraffic {
					reportErr = reporter.ReportUserTraffic(&userTraffic)
				}
			} else if !c.config.DisableUploadTraffic {
				reportErr = errors.New("panel does not implement its declared traffic reporting capability")
			}
		} else if !c.config.DisableUploadTraffic {
			reportErr = errors.New("panel does not support traffic reporting")
		}
		if reportErr != nil {
			observability.TrafficFailures.WithLabelValues(c.panelType).Inc()
			c.logger.Print(reportErr)
		} else {
			c.resetTraffic(&upCounterList, &downCounterList)
		}
	}

	if c.capabilities.OnlineUserReport {
		if reporter, ok := c.apiClient.(api.OnlineUserReporter); ok {
			if onlineDevice, onlineErr := c.GetOnlineDevice(tag); onlineErr != nil {
				c.logger.Print(onlineErr)
			} else if len(*onlineDevice) > 0 {
				if onlineErr = reporter.ReportNodeOnlineUsers(onlineDevice); onlineErr != nil {
					c.logger.Print(onlineErr)
				} else {
					c.logger.Printf("Report %d online users", len(*onlineDevice))
				}
			}
		}
	}

	if c.capabilities.IllegalReport {
		if reporter, ok := c.apiClient.(api.IllegalReporter); ok {
			if detectResult, detectErr := c.GetDetectResult(tag); detectErr != nil {
				c.logger.Print(detectErr)
			} else if len(*detectResult) > 0 {
				if detectErr = reporter.ReportIllegal(detectResult); detectErr != nil {
					c.logger.Print(detectErr)
				} else {
					c.logger.Printf("Report %d illegal behaviors", len(*detectResult))
				}
			}
		}
	}
	return nil
}

func (c *Controller) buildNodeTag(node *api.NodeInfo) string {
	return fmt.Sprintf("%s_%s_%d", node.NodeType, c.config.ListenIP, node.Port)
}

// Check Cert
func (c *Controller) certMonitor() error {
	node := c.currentNode()
	if node != nil && node.EnableTLS && !c.config.EnableREALITY && c.config.CertConfig != nil {
		switch c.config.CertConfig.CertMode {
		case "dns", "http", "tls":
			lego, err := mylego.New(c.config.CertConfig)
			if err != nil {
				c.logger.Print(err)
			}
			// Xray-core supports the OcspStapling certification hot renew
			_, _, _, err = lego.RenewCert()
			if err != nil {
				c.logger.Print(err)
			}
		}
	}
	return nil
}
