package sspanel_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/XrayR-project/XrayR/api"
	"github.com/XrayR-project/XrayR/api/sspanel"
)

// These tests exercise the adapter against an httptest server instead of a live
// panel, so they run in normal `go test ./...` runs (the *_test.go files tagged
// `integration` still hit a real panel and are never executed by CI).

func newOfflineClient(t *testing.T, handler http.HandlerFunc) *sspanel.APIClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return sspanel.New(&api.Config{
		APIHost:  server.URL,
		Key:      "test-key",
		NodeID:   3,
		NodeType: "V2ray",
		Timeout:  5,
	})
}

func TestGetNodeInfoReportsNotModified(t *testing.T) {
	client := newOfflineClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})

	_, err := client.GetNodeInfo()

	require.Error(t, err)
	assert.ErrorIs(t, err, api.ErrNodeNotModified)
}

func TestGetUserListReportsNotModified(t *testing.T) {
	client := newOfflineClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})

	_, err := client.GetUserList()

	require.Error(t, err)
	assert.ErrorIs(t, err, api.ErrUserNotModified)
}

func TestGetNodeRuleReportsNotModified(t *testing.T) {
	client := newOfflineClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})

	_, err := client.GetNodeRule()

	require.Error(t, err)
	assert.ErrorIs(t, err, api.ErrRuleNotModified)
}

func TestPanelFailuresAreClassified(t *testing.T) {
	for name, status := range map[string]int{
		"unauthorized": http.StatusUnauthorized,
		"server error": http.StatusInternalServerError,
	} {
		t.Run(name, func(t *testing.T) {
			client := newOfflineClient(t, func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", status)
			})

			_, err := client.GetNodeInfo()

			var apiErr *api.APIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, 3, apiErr.NodeID)
			assert.NotEmpty(t, apiErr.Panel)
		})
	}
}

func TestGetNodeRuleKeepsLocalRulesAndSkipsInvalidRemotePatterns(t *testing.T) {
	ruleFile := filepath.Join(t.TempDir(), "rulelist")
	require.NoError(t, os.WriteFile(ruleFile, []byte("local-rule\n"), 0o600))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ret":1,"data":[{"id":1,"regex":"remote-rule"},{"id":2,"regex":"(unclosed"}]}`))
	}))
	t.Cleanup(server.Close)

	client := sspanel.New(&api.Config{
		APIHost: server.URL, Key: "test-key", NodeID: 3, NodeType: "V2ray",
		Timeout: 5, RuleListPath: ruleFile,
	})

	rules, err := client.GetNodeRule()

	require.NoError(t, err)
	require.Len(t, *rules, 2)
	assert.Equal(t, "local-rule", (*rules)[0].Pattern.String())
	assert.Equal(t, "remote-rule", (*rules)[1].Pattern.String())
}
