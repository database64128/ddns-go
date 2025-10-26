//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package bsdroute

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"time"
	"unsafe"

	producerpkg "github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/producer/bsdroute/internal/bsdroute"
	"github.com/database64128/ddns-go/producer/internal/broadcaster"
	"github.com/database64128/ddns-go/tslog"
	"golang.org/x/sys/unix"
)

type source struct {
	name string
}

func newSource(name string) (*Source, error) {
	return &Source{source: source{name: name}}, nil
}

func (s *source) snapshot() (producerpkg.Message, error) {
	ioctlFd, err := bsdroute.Socket(unix.AF_INET6, unix.SOCK_DGRAM, 0)
	if err != nil {
		return producerpkg.Message{}, fmt.Errorf("failed to open ioctl socket: %w", err)
	}
	defer unix.Close(ioctlFd)

	b, err := bsdroute.SysctlGetBytes([]int32{unix.CTL_NET, unix.AF_ROUTE, 0, unix.AF_UNSPEC, unix.NET_RT_IFLIST, 0})
	if err != nil {
		return producerpkg.Message{}, fmt.Errorf("failed to get interface dump: %w", err)
	}

	addr4, addr6, ifindex, err := s.handleRouteMessage(ioctlFd, b)
	if err != nil {
		return producerpkg.Message{}, fmt.Errorf("failed to handle route message: %w", err)
	}
	if ifindex == 0 {
		return producerpkg.Message{}, fmt.Errorf("no such network interface: %q", s.name)
	}

	return producerpkg.Message{
		IPv4: addr4,
		IPv6: addr6,
	}, nil
}

func (s *source) handleRouteMessage(ioctlFd int, b []byte) (addr4, addr6 netip.Addr, ifindex uint16, err error) {
	for len(b) >= bsdroute.SizeofMsghdr {
		m := (*bsdroute.Msghdr)(unsafe.Pointer(unsafe.SliceData(b)))
		if m.Msglen < bsdroute.SizeofMsghdr || int(m.Msglen) > len(b) {
			return addr4, addr6, ifindex, fmt.Errorf("invalid routing message length: %d", m.Msglen)
		}
		msgBuf := b[:m.Msglen]
		b = b[m.Msglen:]

		if m.Version != unix.RTM_VERSION {
			return addr4, addr6, ifindex, fmt.Errorf("unsupported routing message version: %d", m.Version)
		}

		switch m.Type {
		case unix.RTM_IFINFO:
			if len(msgBuf) < unix.SizeofIfMsghdr {
				return addr4, addr6, ifindex, fmt.Errorf("invalid if_msghdr length: %d", len(msgBuf))
			}

			ifm := (*unix.IfMsghdr)(unsafe.Pointer(m))

			addrsBuf, ok := m.AddrsBuf(msgBuf, unix.SizeofIfMsghdr)
			if !ok {
				return addr4, addr6, ifindex, fmt.Errorf("invalid ifm_hdrlen: %d", m.HeaderLen())
			}

			var addrs [unix.RTAX_MAX]*unix.RawSockaddr
			bsdroute.ParseAddrs(&addrs, addrsBuf, ifm.Addrs)

			ifpAddr := ifnameFromSockaddr(addrs[unix.RTAX_IFP])
			if string(ifpAddr) == s.name {
				ifindex = ifm.Index
			}

		case unix.RTM_NEWADDR:
			if len(msgBuf) < unix.SizeofIfaMsghdr {
				return addr4, addr6, ifindex, fmt.Errorf("invalid ifa_msghdr length: %d", len(msgBuf))
			}

			ifam := (*unix.IfaMsghdr)(unsafe.Pointer(m))
			if ifam.Index != ifindex {
				continue
			}

			addrsBuf, ok := m.AddrsBuf(msgBuf, unix.SizeofIfaMsghdr)
			if !ok {
				return addr4, addr6, ifindex, fmt.Errorf("invalid ifam_hdrlen: %d", m.HeaderLen())
			}

			var addrs [unix.RTAX_MAX]*unix.RawSockaddr
			bsdroute.ParseAddrs(&addrs, addrsBuf, ifam.Addrs)

			ifaAddr := ipFromSockaddr(addrs[unix.RTAX_IFA])
			if !ifaAddr.IsValid() || ifaAddr.IsLinkLocalUnicast() {
				continue
			}

			if ifaAddr.Is4() {
				addr4 = ifaAddr
			} else {
				ifaFlags, err := bsdroute.IoctlGetIfaFlagInet6(ioctlFd, s.name, (*unix.RawSockaddrInet6)(unsafe.Pointer(addrs[unix.RTAX_IFA])))
				if err != nil {
					return addr4, addr6, ifindex, fmt.Errorf("failed to get interface IPv6 address flags: %w", err)
				}
				if ifaFlags&bsdroute.IN6_IFF_DEPRECATED != 0 ||
					ifaFlags&bsdroute.IN6_IFF_TEMPORARY != 0 {
					continue
				}
				addr6 = ifaAddr
			}
		}
	}

	return addr4, addr6, ifindex, nil
}

func ipFromSockaddr(sa *unix.RawSockaddr) netip.Addr {
	if sa == nil {
		return netip.Addr{}
	}

	switch sa.Family {
	case unix.AF_INET:
		if sa.Len < unix.SizeofSockaddrInet4 {
			return netip.Addr{}
		}
		sa4 := (*unix.RawSockaddrInet4)(unsafe.Pointer(sa))
		return netip.AddrFrom4(sa4.Addr)

	case unix.AF_INET6:
		if sa.Len < unix.SizeofSockaddrInet6 {
			return netip.Addr{}
		}
		sa6 := (*unix.RawSockaddrInet6)(unsafe.Pointer(sa))
		return netip.AddrFrom16(sa6.Addr) // We don't need zone info here.

	default:
		return netip.Addr{}
	}
}

func ifindexFromSockaddr(sa *unix.RawSockaddr) uint16 {
	if sa == nil || sa.Len < unix.SizeofSockaddrDatalink || sa.Family != unix.AF_LINK {
		return 0
	}
	return (*unix.RawSockaddrDatalink)(unsafe.Pointer(sa)).Index
}

func ifnameFromSockaddr(sa *unix.RawSockaddr) []byte {
	if sa != nil && sa.Len >= unix.SizeofSockaddrDatalink && sa.Family == unix.AF_LINK {
		if sa := (*unix.RawSockaddrDatalink)(unsafe.Pointer(sa)); int(sa.Nlen) <= len(sa.Data) {
			return unsafe.Slice((*byte)(unsafe.Pointer(&sa.Data)), sa.Nlen)
		}
	}
	return nil
}

func (cfg *ProducerConfig) newProducer(logger *tslog.Logger) (*Producer, error) {
	if cfg.Interface == "" {
		return nil, errors.New("interface name is required")
	}
	return &Producer{
		producer: producer{
			logger:      logger,
			broadcaster: broadcaster.New(),
			ifname:      cfg.Interface,
		},
	}, nil
}

type producer struct {
	logger      *tslog.Logger
	broadcaster *broadcaster.Broadcaster
	addr4       netip.Addr
	addr6       netip.Addr
	ifname      string
	ifindex     uint16
}

func (p *producer) subscribe() <-chan producerpkg.Message {
	return p.broadcaster.Subscribe()
}

// aLongTimeAgo is a non-zero time, far in the past, used for immediate deadlines.
var aLongTimeAgo = time.Unix(0, 0)

func (p *producer) run(ctx context.Context) {
	f, err := bsdroute.OpenRoutingSocket()
	if err != nil {
		p.logger.Error("Failed to open routing socket", tslog.Err(err))
		return
	}
	defer f.Close()

	ioctlFd, err := bsdroute.Socket(unix.AF_INET6, unix.SOCK_DGRAM, 0)
	if err != nil {
		p.logger.Error("Failed to open ioctl socket", tslog.Err(err))
		return
	}
	defer unix.Close(ioctlFd)

	b, err := bsdroute.SysctlGetBytes([]int32{unix.CTL_NET, unix.AF_ROUTE, 0, unix.AF_UNSPEC, unix.NET_RT_IFLIST, 0})
	if err != nil {
		p.logger.Error("Failed to get interface dump", tslog.Err(err))
		return
	}

	p.logger.Info("Started monitoring network interface", slog.String("interface", p.ifname))

	p.handleAndBroadcast(ioctlFd, b)

	if ctxDone := ctx.Done(); ctxDone != nil {
		go func() {
			<-ctxDone
			if err := f.SetReadDeadline(aLongTimeAgo); err != nil {
				p.logger.Error("Failed to set read deadline on routing socket", tslog.Err(err))
			}
		}()
	}

	p.monitorRoutingSocket(f, ioctlFd)

	p.logger.Info("Stopped monitoring network interface", slog.String("interface", p.ifname))
}

func (p *producer) monitorRoutingSocket(f *os.File, ioctlFd int) {
	// route(8) monitor uses this buffer size.
	// Each read only returns a single message.
	const readBufSize = 2048
	b := make([]byte, readBufSize)
	for {
		n, err := f.Read(b)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				break
			}
			p.logger.Error("Failed to read from routing socket", tslog.Err(err))
			continue
		}
		p.handleAndBroadcast(ioctlFd, b[:n])
	}
}

func (p *producer) handleAndBroadcast(ioctlFd int, b []byte) {
	if p.handleRouteMessage(ioctlFd, b) {
		if p.logger.Enabled(slog.LevelInfo) {
			p.logger.Info("Broadcasting interface IP addresses",
				tslog.Uint("ifindex", p.ifindex),
				tslog.Addr("v4", p.addr4),
				tslog.Addr("v6", p.addr6),
			)
		}

		p.broadcaster.Broadcast(producerpkg.Message{
			IPv4: p.addr4,
			IPv6: p.addr6,
		})
	}
}

func (p *producer) handleRouteMessage(ioctlFd int, b []byte) (updated bool) {
	for len(b) >= bsdroute.SizeofMsghdr {
		m := (*bsdroute.Msghdr)(unsafe.Pointer(unsafe.SliceData(b)))
		if m.Msglen < bsdroute.SizeofMsghdr || int(m.Msglen) > len(b) {
			p.logger.Error("Invalid routing message length",
				tslog.Uint("msglen", m.Msglen),
				tslog.Uint("version", m.Version),
				slog.Any("type", bsdroute.MsgType(m.Type)),
			)
			return
		}
		msgBuf := b[:m.Msglen]
		b = b[m.Msglen:]

		if m.Version != unix.RTM_VERSION {
			p.logger.Warn("Unsupported routing message version",
				tslog.Uint("msglen", m.Msglen),
				tslog.Uint("version", m.Version),
				slog.Any("type", bsdroute.MsgType(m.Type)),
			)
			continue
		}

		switch m.Type {
		case unix.RTM_IFINFO:
			if len(msgBuf) < unix.SizeofIfMsghdr {
				p.logger.Error("Invalid if_msghdr length",
					tslog.Uint("msglen", m.Msglen),
					tslog.Uint("version", m.Version),
					slog.Any("type", bsdroute.MsgType(m.Type)),
				)
				return
			}

			ifm := (*unix.IfMsghdr)(unsafe.Pointer(m))

			addrsBuf, ok := m.AddrsBuf(msgBuf, unix.SizeofIfMsghdr)
			if !ok {
				p.logger.Error("Invalid ifm_hdrlen",
					tslog.Uint("msglen", m.Msglen),
					tslog.Uint("version", m.Version),
					slog.Any("type", bsdroute.MsgType(m.Type)),
					tslog.Uint("hdrlen", m.HeaderLen()),
				)
				return
			}

			var addrs [unix.RTAX_MAX]*unix.RawSockaddr
			bsdroute.ParseAddrs(&addrs, addrsBuf, ifm.Addrs)
			ifpAddr := ifnameFromSockaddr(addrs[unix.RTAX_IFP])

			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Processing if_msghdr",
					slog.Any("flags", bsdroute.IfaceFlags(ifm.Flags)),
					tslog.Uint("index", ifm.Index),
					tslog.ByteString("ifpAddr", ifpAddr),
				)
			}

			if string(ifpAddr) == p.ifname {
				if ifm.Flags&unix.IFF_UP != 0 {
					if p.logger.Enabled(slog.LevelInfo) {
						p.logger.Info("Found interface",
							tslog.ByteString("name", ifpAddr),
							tslog.Uint("index", ifm.Index),
						)
					}
					p.ifindex = ifm.Index
				} else {
					if p.logger.Enabled(slog.LevelInfo) {
						p.logger.Info("Lost interface",
							tslog.ByteString("name", ifpAddr),
							tslog.Uint("index", ifm.Index),
						)
					}
					p.ifindex = 0
				}
			}

		case unix.RTM_NEWADDR, unix.RTM_DELADDR:
			if len(msgBuf) < unix.SizeofIfaMsghdr {
				p.logger.Error("Invalid ifa_msghdr length",
					tslog.Uint("msglen", m.Msglen),
					tslog.Uint("version", m.Version),
					slog.Any("type", bsdroute.MsgType(m.Type)),
				)
				return
			}

			ifam := (*unix.IfaMsghdr)(unsafe.Pointer(m))
			if ifam.Index != p.ifindex {
				continue
			}

			addrsBuf, ok := m.AddrsBuf(msgBuf, unix.SizeofIfaMsghdr)
			if !ok {
				p.logger.Error("Invalid ifam_hdrlen",
					tslog.Uint("msglen", m.Msglen),
					tslog.Uint("version", m.Version),
					slog.Any("type", bsdroute.MsgType(m.Type)),
					tslog.Uint("hdrlen", m.HeaderLen()),
				)
				return
			}

			var addrs [unix.RTAX_MAX]*unix.RawSockaddr
			bsdroute.ParseAddrs(&addrs, addrsBuf, ifam.Addrs)

			ifaAddr := ipFromSockaddr(addrs[unix.RTAX_IFA])
			if !ifaAddr.IsValid() || ifaAddr.IsLinkLocalUnicast() {
				continue
			}

			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Processing ifa_msghdr",
					slog.Any("type", bsdroute.MsgType(ifam.Type)),
					tslog.Uint("index", ifam.Index),
					tslog.Addr("ifaAddr", ifaAddr),
				)
			}

			switch m.Type {
			case unix.RTM_NEWADDR:
				switch {
				case ifaAddr.Is4() && ifaAddr != p.addr4:
					if p.logger.Enabled(slog.LevelDebug) {
						p.logger.Debug("Updating cached IPv4 address",
							tslog.Addr("oldAddr", p.addr4),
							tslog.Addr("newAddr", ifaAddr),
						)
					}
					p.addr4 = ifaAddr
					updated = true

				case ifaAddr.Is6() && ifaAddr != p.addr6:
					ifaFlags, err := bsdroute.IoctlGetIfaFlagInet6(ioctlFd, p.ifname, (*unix.RawSockaddrInet6)(unsafe.Pointer(addrs[unix.RTAX_IFA])))
					if err != nil {
						p.logger.Error("Failed to get interface IPv6 address flags",
							tslog.Uint("index", ifam.Index),
							tslog.Addr("addr", ifaAddr),
							tslog.Err(err),
						)
						continue
					}

					if ifaFlags&bsdroute.IN6_IFF_DEPRECATED != 0 ||
						ifaFlags&bsdroute.IN6_IFF_TEMPORARY != 0 {
						continue
					}

					if p.logger.Enabled(slog.LevelDebug) {
						p.logger.Debug("Updating cached IPv6 address",
							tslog.Addr("oldAddr", p.addr6),
							tslog.Addr("newAddr", ifaAddr),
						)
					}

					p.addr6 = ifaAddr
					updated = true
				}

			case unix.RTM_DELADDR:
				switch ifaAddr {
				case p.addr4:
					if p.logger.Enabled(slog.LevelDebug) {
						p.logger.Debug("Removing cached IPv4 address",
							tslog.Addr("addr", ifaAddr),
						)
					}
					p.addr4 = netip.Addr{}
					updated = true

				case p.addr6:
					if p.logger.Enabled(slog.LevelDebug) {
						p.logger.Debug("Removing cached IPv6 address",
							tslog.Addr("addr", ifaAddr),
						)
					}
					p.addr6 = netip.Addr{}
					updated = true
				}

			default:
				panic("unreachable")
			}

		case unix.RTM_DELETE:
			if len(msgBuf) < unix.SizeofRtMsghdr {
				p.logger.Error("Invalid rt_msghdr length",
					tslog.Uint("msglen", m.Msglen),
					tslog.Uint("version", m.Version),
					slog.Any("type", bsdroute.MsgType(m.Type)),
				)
				return
			}

			rtm := (*unix.RtMsghdr)(unsafe.Pointer(m))
			if rtm.Flags&unix.RTF_STATIC == 0 {
				continue
			}

			addrsBuf, ok := m.AddrsBuf(msgBuf, unix.SizeofRtMsghdr)
			if !ok {
				p.logger.Error("Invalid rtm_hdrlen",
					tslog.Uint("msglen", m.Msglen),
					tslog.Uint("version", m.Version),
					slog.Any("type", bsdroute.MsgType(m.Type)),
					tslog.Uint("hdrlen", m.HeaderLen()),
				)
				return
			}

			var addrs [unix.RTAX_MAX]*unix.RawSockaddr
			bsdroute.ParseAddrs(&addrs, addrsBuf, rtm.Addrs)

			dstAddr := ipFromSockaddr(addrs[unix.RTAX_DST])
			if dstAddr != p.addr4 {
				continue
			}

			gatewayAddr := ifindexFromSockaddr(addrs[unix.RTAX_GATEWAY])
			if gatewayAddr != p.ifindex {
				continue
			}

			ifaAddr := ipFromSockaddr(addrs[unix.RTAX_IFA])
			if ifaAddr != p.addr4 {
				continue
			}

			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Processing rt_msghdr",
					slog.Any("type", bsdroute.MsgType(rtm.Type)),
					tslog.Uint("index", rtm.Index),
					slog.Any("flags", bsdroute.RouteFlags(rtm.Flags)),
					tslog.Int("pid", rtm.Pid),
					tslog.Int("seq", rtm.Seq),
					tslog.Addr("dstAddr", dstAddr),
					tslog.Uint("gatewayAddr", gatewayAddr),
					tslog.Addr("ifaAddr", ifaAddr),
				)
			}

			// On macOS, in some scenarios, an interface IPv4 address can go away
			// without an RTM_DELADDR message. Here we deduce the address removal
			// from the deletion of its corresponding static route.
			p.addr4 = netip.Addr{}
			updated = true
		}
	}

	return updated
}
