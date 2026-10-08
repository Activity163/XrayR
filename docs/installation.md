# Installation

XrayR needs three things in the same directory: `config.yml`, and the
`geoip.dat` / `geosite.dat` rule files.

The rule files are **not** stored in this repository — together they are ~14 MB and
they change constantly, so a stale copy silently breaks `geoip:` / `geosite:`
routing rules. Every install downloads the newest release from
[Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat) and
verifies the published sha256 checksum:

```bash
bash release/download-rules-dat.sh <output_dir>
```

The script uses `RULES_DAT_REPO` and `RULES_DAT_BASE_URL` for overrides.

## Binary

1. Install the binary and the systemd unit:

```bash
sudo install -m 0755 XrayR /usr/local/bin/XrayR
sudo mkdir -p /etc/XrayR
sudo install -m 0644 release/systemd/XrayR.service /etc/systemd/system/XrayR.service
```

2. Create `/etc/XrayR/config.yml` — `XrayR config init` writes one interactively or
   from flags — then download the rule data next to it:

```bash
sudo bash release/download-rules-dat.sh /etc/XrayR
```

3. Validate the configuration and start the service:

```bash
XrayR config check -c /etc/XrayR/config.yml
sudo systemctl enable --now XrayR
```

`ExecStartPre` runs the same check on every start and restart, so an invalid
configuration file stops the unit instead of leaving a half-running node behind.

### Refreshing the rule data

The rule files are plain data and can be updated without touching the binary:

```bash
sudo bash release/download-rules-dat.sh /etc/XrayR
sudo systemctl restart XrayR
```

## Docker

The image already contains `geoip.dat` / `geosite.dat`, fetched from the latest
release when the image is built. Mount the configuration read-only and persist the
cache directory:

```yaml
services:
  xrayr:
    image: ghcr.io/xrayr-project/xrayr:latest
    restart: unless-stopped
    volumes:
      - ./config.yml:/etc/XrayR/config.yml:ro
      - ./cache:/etc/XrayR/cache
    network_mode: host
```

To pin a specific rule snapshot instead of the one baked into the image, mount your
own copies over the built-in files:

```yaml
    volumes:
      - ./config.yml:/etc/XrayR/config.yml:ro
      - ./geoip.dat:/etc/XrayR/geoip.dat:ro
      - ./geosite.dat:/etc/XrayR/geosite.dat:ro
      - ./cache:/etc/XrayR/cache
```

Run `XrayR config check` before starting a replacement container.
