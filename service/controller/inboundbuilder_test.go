package controller_test

import (
	"strings"
	"testing"

	"github.com/XrayR-project/XrayR/api"
	"github.com/XrayR-project/XrayR/common/mylego"
	. "github.com/XrayR-project/XrayR/service/controller"
)

func TestBuildV2ray(t *testing.T) {
	nodeInfo := &api.NodeInfo{
		NodeType:          "V2ray",
		NodeID:            1,
		Port:              1145,
		SpeedLimit:        0,
		AlterID:           2,
		TransportProtocol: "ws",
		Host:              "test.test.tk",
		Path:              "v2ray",
		EnableTLS:         false,
	}
	certConfig := &mylego.CertConfig{
		CertMode:   "http",
		CertDomain: "test.test.tk",
		Provider:   "alidns",
		Email:      "test@gmail.com",
	}
	config := &Config{
		CertConfig: certConfig,
	}
	_, err := InboundBuilder(config, nodeInfo, "test_tag")
	if err != nil {
		t.Error(err)
	}
}

func TestBuildTrojan(t *testing.T) {
	nodeInfo := &api.NodeInfo{
		NodeType:          "Trojan",
		NodeID:            1,
		Port:              1145,
		SpeedLimit:        0,
		AlterID:           2,
		TransportProtocol: "tcp",
		Host:              "trojan.test.tk",
		Path:              "v2ray",
		EnableTLS:         false,
	}
	DNSEnv := make(map[string]string)
	DNSEnv["ALICLOUD_ACCESS_KEY"] = "aaa"
	DNSEnv["ALICLOUD_SECRET_KEY"] = "bbb"
	certConfig := &mylego.CertConfig{
		CertMode:   "dns",
		CertDomain: "trojan.test.tk",
		Provider:   "alidns",
		Email:      "test@gmail.com",
		DNSEnv:     DNSEnv,
	}
	config := &Config{
		CertConfig: certConfig,
	}
	_, err := InboundBuilder(config, nodeInfo, "test_tag")
	if err != nil {
		t.Error(err)
	}
}

func TestBuildSS(t *testing.T) {
	nodeInfo := &api.NodeInfo{
		NodeType:          "Shadowsocks",
		NodeID:            1,
		Port:              1145,
		SpeedLimit:        0,
		AlterID:           2,
		CypherMethod:      "aes-256-gcm",
		TransportProtocol: "tcp",
		Host:              "test.test.tk",
		Path:              "v2ray",
		EnableTLS:         false,
	}
	DNSEnv := make(map[string]string)
	DNSEnv["ALICLOUD_ACCESS_KEY"] = "aaa"
	DNSEnv["ALICLOUD_SECRET_KEY"] = "bbb"
	certConfig := &mylego.CertConfig{
		CertMode:   "dns",
		CertDomain: "trojan.test.tk",
		Provider:   "alidns",
		Email:      "test@me.com",
		DNSEnv:     DNSEnv,
	}
	config := &Config{
		CertConfig: certConfig,
	}
	_, err := InboundBuilder(config, nodeInfo, "test_tag")
	if err != nil {
		t.Error(err)
	}
}

// ssNode builds a Shadowsocks node with the given cipher.
func ssNode(cipher string) *api.NodeInfo {
	return &api.NodeInfo{
		NodeType:          "Shadowsocks",
		NodeID:            1,
		Port:              1145,
		CypherMethod:      cipher,
		ServerKey:         "R3Xq0kQmYb2vN8pH4tZ6wL1sJ5jF9cA7gD3eV8bM2K4=",
		TransportProtocol: "tcp",
	}
}

// TestBuildSS2022AcceptsTheAESMethods covers the only ciphers xray-core allows in
// SS-2022 multi-user mode.
func TestBuildSS2022AcceptsTheAESMethods(t *testing.T) {
	for _, cipher := range []string{"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm"} {
		if _, err := InboundBuilder(&Config{}, ssNode(cipher), "test_tag"); err != nil {
			t.Errorf("cipher %s: unexpected error: %v", cipher, err)
		}
	}
}

// TestBuildSS2022RejectsChacha20 pins the constraint xray-core enforces only when
// it builds the inbound. XrayR always runs SS-2022 in multi-user mode, because it
// adds and removes one account per panel user and only the multi-user inbound
// implements proxy.UserManager. That mode rejects every method that is not
// blake3-aes-*-gcm. Without the check here the node died with an opaque
// "failed to build inbound handler for protocol shadowsocks" that named neither
// the cipher nor the replacement.
func TestBuildSS2022RejectsChacha20(t *testing.T) {
	_, err := InboundBuilder(&Config{}, ssNode("2022-blake3-chacha20-poly1305"), "test_tag")
	if err == nil {
		t.Fatal("expected an error for a non-AES SS-2022 cipher")
	}
	for _, want := range []string{"2022-blake3-chacha20-poly1305", "blake3-aes", "2022-blake3-aes-128-gcm"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message %q does not mention %q", err.Error(), want)
		}
	}
}

// TestBuildSSAllowsEveryNon2022Cipher checks the new guard stays out of the
// classic AEAD path, where chacha20-poly1305 is perfectly valid.
func TestBuildSSAllowsEveryNon2022Cipher(t *testing.T) {
	for _, cipher := range []string{"aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-poly1305"} {
		if _, err := InboundBuilder(&Config{}, ssNode(cipher), "test_tag"); err != nil {
			t.Errorf("cipher %s: unexpected error: %v", cipher, err)
		}
	}
}
