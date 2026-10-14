# Supported panel

XrayR talks to exactly one panel backend: **Xboard**.

| `PanelType` | Adapter | Accepted node types |
|---|---|---|
| `Xboard` | `NewV2board` | `V2ray`, `Vmess`, `Vless`, `Trojan`, `Shadowsocks` |

`NewV2board` and `V2board` are accepted as historical aliases of `Xboard`, so
existing configurations keep working. Write `Xboard` in new configurations.

A minimal node block is:

```yaml
Nodes:
  - PanelType: Xboard
    ApiConfig:
      ApiHost: https://panel.example.com
      ApiKey: CHANGE_ME
      NodeID: 1
      NodeType: Vless
      Timeout: 30
```

Anything else in `PanelType` is rejected at startup with a message naming the
closest supported value:

```
unsupported panel type "Xbord"; did you mean "Xboard"? supported panels: Xboard
```

Run `XrayR config check` to validate the panel type and node type before
starting the service, and `XrayR doctor` for a full preflight report.

See [Xboard](dui-jie-xboard/xboard.md) for the panel-specific setup steps.
