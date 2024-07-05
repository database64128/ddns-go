# DDNS Service

[![Go Reference](https://pkg.go.dev/badge/github.com/database64128/ddns-go.svg)](https://pkg.go.dev/github.com/database64128/ddns-go)
[![Test](https://github.com/database64128/ddns-go/actions/workflows/test.yml/badge.svg)](https://github.com/database64128/ddns-go/actions/workflows/test.yml)
[![Release](https://github.com/database64128/ddns-go/actions/workflows/release.yml/badge.svg)](https://github.com/database64128/ddns-go/actions/workflows/release.yml)

🌐 DDNS service supporting dynamic updates of A, AAAA, and HTTPS records.

## Features

- Multiple IP address sources
    - Obtain WAN IPv4 address from ASUS router
    - Obtain public IPv4 and IPv6 addresses from IP address APIs
    - Obtain network interface IPv4 and IPv6 addresses
- Manage DNS records with Cloudflare API
    - Update A and AAAA records
    - Update HTTPS records

## Configuration

The configuration format is [documented in code](https://pkg.go.dev/github.com/database64128/ddns-go/service#Config).

To get started, take a look at the [example configuration file](docs/config.json).

## License

[GPLv3](LICENSE)
