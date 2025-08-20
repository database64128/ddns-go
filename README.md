# DDNS Service

[![Go Reference](https://pkg.go.dev/badge/github.com/database64128/ddns-go.svg)](https://pkg.go.dev/github.com/database64128/ddns-go)
[![Test](https://github.com/database64128/ddns-go/actions/workflows/test.yml/badge.svg)](https://github.com/database64128/ddns-go/actions/workflows/test.yml)
[![Release](https://github.com/database64128/ddns-go/actions/workflows/release.yml/badge.svg)](https://github.com/database64128/ddns-go/actions/workflows/release.yml)

🌐 Over-engineered DDNS service with native OS integrations for managing A, AAAA, and HTTPS records.

## Features

- Multiple IP address sources
    - `"asusrouter"`: Obtain WAN IPv4 address from ASUS router.
    - `"ipapi"`: Obtain public IPv4 and IPv6 addresses from IP address APIs.
    - Monitor network interface IPv4 and IPv6 addresses
        - `"netlink"`: Use Linux's Netlink interface.
        - `"bsdroute"`: Use the routing socket (`route(4)`) on macOS, DragonFly BSD, FreeBSD, NetBSD, and OpenBSD.
        - `"win32iphlp"`: Use Windows IP Helper API.
        - `"iface"`: Generic implementation with periodic polling.
- Manage DNS records with Cloudflare API
    - Update A and AAAA records
    - Update HTTPS records

## Deployment

### Arch Linux package

Release and VCS packages are available in the AUR:

[![cubic-ddns-go AUR package](https://img.shields.io/aur/version/cubic-ddns-go?label=cubic-ddns-go)](https://aur.archlinux.org/packages/cubic-ddns-go)
[![cubic-ddns-go-git AUR package](https://img.shields.io/aur/version/cubic-ddns-go-git?label=cubic-ddns-go-git)](https://aur.archlinux.org/packages/cubic-ddns-go-git)

### Prebuilt binaries

Download from [releases](https://github.com/database64128/ddns-go/releases).

### Build from source

Build and install the latest version using Go:

```sh
go install github.com/database64128/ddns-go/cmd/ddns-go@latest
```

Or clone the repository and build it manually:

```sh
go build -trimpath -ldflags '-s -w' ./cmd/ddns-go
```

## Configuration

The configuration format is [documented in code](https://pkg.go.dev/github.com/database64128/ddns-go/service#Config).

To get started, take a look at the [example configuration file](docs/config.json).

## License

[GPLv3](LICENSE)
