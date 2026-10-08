package panel

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
)

// TestRuleDataLoads is the end-to-end check for the shipped routing template.
//
// geoip.dat / geosite.dat are not stored in the repository: they are fetched on
// every install by release/download-rules-dat.sh. This test builds the exact
// release/config/route.json against a directory holding those files and starts a
// real xray-core instance, so a template that references a rule set the downloaded
// data does not provide fails here instead of on a user's node.
//
// It is skipped unless RULES_DAT_DIR points at a directory containing geoip.dat and
// geosite.dat, so `go test ./...` stays offline-friendly:
//
//	bash release/download-rules-dat.sh /tmp/ruledat
//	RULES_DAT_DIR=/tmp/ruledat go test ./panel/
func TestRuleDataLoads(t *testing.T) {
	assetDir := os.Getenv("RULES_DAT_DIR")
	if assetDir == "" {
		t.Skip("RULES_DAT_DIR is not set; run release/download-rules-dat.sh first")
	}
	t.Setenv("XRAY_LOCATION_ASSET", assetDir)

	raw, err := os.ReadFile("../release/config/route.json")
	if err != nil {
		t.Fatalf("read route.json: %v", err)
	}
	routerConf := &conf.RouterConfig{}
	if err := json.Unmarshal(raw, routerConf); err != nil {
		t.Fatalf("unmarshal route.json: %v", err)
	}
	routeConfig, err := routerConf.Build()
	if err != nil {
		t.Fatalf("build route config against %s: %v", assetDir, err)
	}

	cfg := &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.InboundConfig{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(routeConfig),
		},
	}
	instance, err := core.New(cfg)
	if err != nil {
		t.Fatalf("create core instance against %s: %v", assetDir, err)
	}
	if err := instance.Start(); err != nil {
		t.Fatalf("start core instance against %s: %v", assetDir, err)
	}
	if err := instance.Close(); err != nil {
		t.Fatalf("close core instance: %v", err)
	}
}
