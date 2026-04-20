package netlink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	producerpkg "github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/producer/internal/broadcaster"
	"github.com/database64128/ddns-go/producer/netlink/internal/rtnetlink"
	"github.com/database64128/ddns-go/tslog"
	"golang.org/x/sys/unix"
)

func (cfg *ProducerConfig) newProducer(logger *tslog.Logger) (*Producer, error) {
	if cfg.Interface == "" {
		return nil, errors.New("interface name is required")
	}

	sockOpts := rtnetlink.SocketOptions{
		SendBufferSize:    cfg.SocketSendBufferSize,
		ReceiveBufferSize: cfg.SocketReceiveBufferSize,
	}

	var rulemgr *ruleManager
	if cfg.FromAddrLookupMain {
		rulemgr = &ruleManager{
			updateCh: make(chan addrUpdate),
			conn: conn{
				logger:        logger,
				socketOptions: sockOpts,
				respCh:        make(chan response),
			},
		}
	}

	return &Producer{
		producer: producer{
			conn: conn{
				logger:        logger,
				socketOptions: sockOpts,
				respCh:        make(chan response),
			},
			broadcaster: broadcaster.New(),
			ruleManager: rulemgr,
			resyncCh:    make(chan struct{}),
			ifname:      cfg.Interface,
		},
	}, nil
}

type producer struct {
	conn
	wg          sync.WaitGroup
	broadcaster *broadcaster.Broadcaster
	ruleManager *ruleManager
	resyncCh    chan struct{}
	ifname      string
	ifindex     uint32
	addr4       netip.Addr
	addr6       netip.Addr
}

type conn struct {
	logger        *tslog.Logger
	socketOptions rtnetlink.SocketOptions
	respCh        chan response
	nlConn        *rtnetlink.Conn
	seq           uint32
}

func (c *conn) openNlConn(groups uint32) (err error) {
	c.nlConn, err = c.socketOptions.Open(groups)
	return err
}

func (c *conn) closeNlConn() error {
	return c.nlConn.Close()
}

type response struct {
	seq uint32
	err int32
}

func (p *producer) subscribe() <-chan producerpkg.Message {
	return p.broadcaster.Subscribe()
}

// aLongTimeAgo is a non-zero time, far in the past, used for immediate deadlines.
var aLongTimeAgo = time.Unix(0, 0)

func (p *producer) run(ctx context.Context) {
	if err := p.openNlConn(unix.RTMGRP_LINK | unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR); err != nil {
		p.logger.Error("Failed to open netlink connection", tslog.Err(err))
		return
	}
	defer p.closeNlConn()

	p.logger.Info("Started monitoring network interface", slog.String("interface", p.ifname))

	_ = context.AfterFunc(ctx, func() {
		if err := p.nlConn.SetReadDeadline(aLongTimeAgo); err != nil {
			p.logger.Error("Failed to set read deadline on netlink connection", tslog.Err(err))
		}
	})

	if p.ruleManager != nil {
		if err := p.ruleManager.Start(ctx, &p.wg); err != nil {
			p.logger.Error("Failed to start rule manager", tslog.Err(err))
			return
		}
	}

	p.wg.Go(func() {
		p.readAndHandle(ctx)
		close(p.respCh)   // unblock sendAndWait and thus handleStateSyncs
		close(p.resyncCh) // unblock handleStateSyncs
		if p.ruleManager != nil {
			p.ruleManager.Stop()
		}
	})

	p.handleStateSyncs()
	p.wg.Wait()

	p.logger.Info("Stopped monitoring network interface", slog.String("interface", p.ifname))
}

const readBufSize = 32 * 1024

func (p *producer) readAndHandle(ctx context.Context) {
	var rsa unix.RawSockaddrNetlink
	rb := make([]byte, readBufSize)
	riov := unix.Iovec{
		Base: unsafe.SliceData(rb),
		Len:  readBufSize,
	}
	rmsg := unix.Msghdr{
		Name:    (*byte)(unsafe.Pointer(&rsa)),
		Namelen: unix.SizeofSockaddrNetlink,
		Iov:     &riov,
		Iovlen:  1,
	}
	rc := p.nlConn.NewRConn()

	for {
		n, err := rc.ReadMsg(&rmsg, 0)
		if err != nil {
			if se, ok := errors.AsType[*os.SyscallError](err); ok && se.Err == syscall.ENOBUFS {
				p.logger.Warn("Netlink socket receive buffer ran out, resyncing state in 5 seconds")
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
				p.resyncCh <- struct{}{}
				continue
			}
			if errors.Is(err, os.ErrDeadlineExceeded) {
				break
			}
			p.logger.Error("Failed to read netlink message", tslog.Err(err))
			continue
		}

		if p.logger.Enabled(slog.LevelDebug) {
			p.logger.Debug("Received netlink message",
				tslog.Int("n", n),
				tslog.Uint("nl_pid", rsa.Pid),
				tslog.Uint("nl_groups", rsa.Groups),
			)
		}

		addr4updated, addr6updated := p.handleNetlinkMessage(rb[:n])
		if !addr4updated && !addr6updated {
			continue
		}

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

func (p *producer) handleNetlinkMessage(b []byte) (addr4updated, addr6updated bool) {
	for len(b) >= unix.SizeofNlMsghdr {
		nlh := (*unix.NlMsghdr)(unsafe.Pointer(unsafe.SliceData(b)))
		msgSize := rtnetlink.NlMsgAlign(nlh.Len)
		if nlh.Len < unix.SizeofNlMsghdr || int(msgSize) > len(b) {
			p.logger.Error("Invalid netlink message length",
				tslog.Uint("nlmsg_len", nlh.Len),
				tslog.Uint("nlmsg_type", nlh.Type),
				tslog.Uint("nlmsg_flags", nlh.Flags),
				tslog.Uint("nlmsg_seq", nlh.Seq),
				tslog.Uint("nlmsg_pid", nlh.Pid),
				tslog.Uint("msgSize", msgSize),
				tslog.Int("len(b)", len(b)),
			)
			return
		}

		switch nlh.Type {
		case unix.NLMSG_DONE:
			p.respCh <- response{seq: nlh.Seq, err: 0}
			return

		case unix.NLMSG_ERROR:
			if nlh.Len < unix.SizeofNlMsghdr+unix.SizeofNlMsgerr {
				p.logger.Error("Invalid NLMSG_ERROR message length",
					tslog.Uint("nlmsg_len", nlh.Len),
					tslog.Uint("nlmsg_type", nlh.Type),
					tslog.Uint("nlmsg_flags", nlh.Flags),
					tslog.Uint("nlmsg_seq", nlh.Seq),
					tslog.Uint("nlmsg_pid", nlh.Pid),
				)
				return
			}

			nle := (*unix.NlMsgerr)(unsafe.Pointer(unsafe.SliceData(b[unix.SizeofNlMsghdr:])))
			msg := p.handleNleAttrs(b[unix.SizeofNlMsghdr+unix.SizeofNlMsgerr : nlh.Len])
			lvl := slog.LevelDebug
			if nle.Error != 0 {
				lvl = slog.LevelError
			}

			if p.logger.Enabled(lvl) {
				p.logger.Log(lvl, "Received response to netlink request",
					tslog.Int("error", nle.Error),
					tslog.Uint("seq", nle.Msg.Seq),
					slog.String("msg", msg),
				)
			}

			p.respCh <- response{seq: nle.Msg.Seq, err: nle.Error}

		case unix.RTM_NEWLINK, unix.RTM_DELLINK:
			if len(b) < unix.SizeofNlMsghdr+unix.SizeofIfInfomsg {
				p.logger.Error("Invalid IfInfomsg message length",
					tslog.Uint("nlmsg_len", nlh.Len),
					tslog.Uint("nlmsg_type", nlh.Type),
					tslog.Uint("nlmsg_flags", nlh.Flags),
					tslog.Uint("nlmsg_seq", nlh.Seq),
					tslog.Uint("nlmsg_pid", nlh.Pid),
				)
				return
			}

			ifim := (*unix.IfInfomsg)(unsafe.Pointer(unsafe.SliceData(b[unix.SizeofNlMsghdr:])))
			name := p.handleIfiAttrs(b[unix.SizeofNlMsghdr+unix.SizeofIfInfomsg : nlh.Len])

			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Parsed IfInfomsg message",
					tslog.Uint("nlmsg_len", nlh.Len),
					tslog.Uint("nlmsg_type", nlh.Type),
					tslog.Uint("nlmsg_flags", nlh.Flags),
					tslog.Uint("nlmsg_seq", nlh.Seq),
					tslog.Uint("nlmsg_pid", nlh.Pid),
					tslog.Uint("ifi_family", ifim.Family),
					tslog.Uint("ifi_type", ifim.Type),
					tslog.Int("ifi_index", ifim.Index),
					tslog.Uint("ifi_flags", ifim.Flags),
					tslog.Uint("ifi_change", ifim.Change),
					slog.String("name", name),
				)
			}

			if name == p.ifname {
				switch nlh.Type {
				case unix.RTM_NEWLINK:
					if p.logger.Enabled(slog.LevelInfo) {
						p.logger.Info("Found interface",
							slog.String("name", name),
							tslog.Int("ifindex", ifim.Index),
						)
					}
					p.ifindex = uint32(ifim.Index)

				case unix.RTM_DELLINK:
					if p.logger.Enabled(slog.LevelInfo) {
						p.logger.Info("Lost interface",
							slog.String("name", name),
							tslog.Int("ifindex", ifim.Index),
						)
					}
					p.ifindex = 0
				}

				// No need to clear the cached addresses, because they will be updated by the address messages.
			}

		case unix.RTM_NEWADDR, unix.RTM_DELADDR:
			if len(b) < unix.SizeofNlMsghdr+unix.SizeofIfAddrmsg {
				p.logger.Error("Invalid IfAddrmsg message length",
					tslog.Uint("nlmsg_len", nlh.Len),
					tslog.Uint("nlmsg_type", nlh.Type),
					tslog.Uint("nlmsg_flags", nlh.Flags),
					tslog.Uint("nlmsg_seq", nlh.Seq),
					tslog.Uint("nlmsg_pid", nlh.Pid),
				)
				return
			}

			ifam := (*unix.IfAddrmsg)(unsafe.Pointer(unsafe.SliceData(b[unix.SizeofNlMsghdr:])))
			if ifam.Index != p.ifindex {
				if p.logger.Enabled(slog.LevelDebug) {
					p.logger.Debug("Skipping address for different interface",
						tslog.Uint("ifa_index", ifam.Index),
						tslog.Uint("ifindex", p.ifindex),
					)
				}
				break
			}

			switch ifam.Family {
			case unix.AF_INET, unix.AF_INET6:
				addr, label, cacheInfo, flags := p.handleIfaAttrs(b[unix.SizeofNlMsghdr+unix.SizeofIfAddrmsg : nlh.Len])

				if p.logger.Enabled(slog.LevelDebug) {
					p.logger.Debug("Parsed IfAddrmsg message",
						tslog.Uint("nlmsg_len", nlh.Len),
						tslog.Uint("nlmsg_type", nlh.Type),
						tslog.Uint("nlmsg_flags", nlh.Flags),
						tslog.Uint("nlmsg_seq", nlh.Seq),
						tslog.Uint("nlmsg_pid", nlh.Pid),
						tslog.Uint("ifa_family", ifam.Family),
						tslog.Uint("ifa_prefixlen", ifam.Prefixlen),
						tslog.Uint("ifa_flags", ifam.Flags),
						tslog.Uint("ifa_scope", ifam.Scope),
						tslog.Uint("ifa_index", ifam.Index),
						tslog.Addr("addr", addr),
						slog.String("label", label),
						tslog.Uint("ifa_prefered", cacheInfo.Prefered),
						tslog.Uint("ifa_valid", cacheInfo.Valid),
						tslog.Uint("cstamp", cacheInfo.Cstamp),
						tslog.Uint("tstamp", cacheInfo.Tstamp),
						tslog.Uint("flags", flags),
					)
				}

				// Skip temporary addresses, as they are inherently temporary.
				//
				// Permit deprecated addresses, because we get notified of address
				// deprecations via RTM_NEWADDR messages, and we need to be able to
				// remove them from our cache.
				if flags&unix.IFA_F_TEMPORARY != 0 {
					break
				}

				// Skip link-local addresses.
				if addr.IsLinkLocalUnicast() {
					break
				}

				switch nlh.Type {
				case unix.RTM_NEWADDR:
					if flags&unix.IFA_F_DEPRECATED != 0 {
						break
					}

					switch {
					case addr.Is4() && addr != p.addr4:
						if p.logger.Enabled(slog.LevelDebug) {
							p.logger.Debug("Updating cached IPv4 address",
								tslog.Addr("oldAddr", p.addr4),
								tslog.Addr("newAddr", addr),
							)
						}
						p.addr4 = addr
						addr4updated = true
						if p.ruleManager != nil {
							p.ruleManager.NotifyAddAddr(addr)
						}

					case addr.Is6() && addr != p.addr6:
						if p.logger.Enabled(slog.LevelDebug) {
							p.logger.Debug("Updating cached IPv6 address",
								tslog.Addr("oldAddr", p.addr6),
								tslog.Addr("newAddr", addr),
							)
						}
						p.addr6 = addr
						addr6updated = true
						if p.ruleManager != nil {
							p.ruleManager.NotifyAddAddr(addr)
						}
					}

				case unix.RTM_DELADDR:
					switch addr {
					case p.addr4:
						if p.logger.Enabled(slog.LevelDebug) {
							p.logger.Debug("Removing cached IPv4 address",
								tslog.Addr("addr", addr),
							)
						}
						p.addr4 = netip.Addr{}
						addr4updated = true
						if p.ruleManager != nil {
							p.ruleManager.NotifyDelAddr(addr)
						}

					case p.addr6:
						if p.logger.Enabled(slog.LevelDebug) {
							p.logger.Debug("Removing cached IPv6 address",
								tslog.Addr("addr", addr),
							)
						}
						p.addr6 = netip.Addr{}
						addr6updated = true
						if p.ruleManager != nil {
							p.ruleManager.NotifyDelAddr(addr)
						}
					}
				}

			default:
				if p.logger.Enabled(slog.LevelDebug) {
					p.logger.Debug("Skipping address with unsupported family",
						tslog.Uint("ifa_family", ifam.Family),
						tslog.Uint("ifa_prefixlen", ifam.Prefixlen),
						tslog.Uint("ifa_flags", ifam.Flags),
						tslog.Uint("ifa_scope", ifam.Scope),
						tslog.Uint("ifa_index", ifam.Index),
					)
				}
			}

		default:
			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Skipping netlink message",
					tslog.Uint("nlmsg_len", nlh.Len),
					tslog.Uint("nlmsg_type", nlh.Type),
					tslog.Uint("nlmsg_flags", nlh.Flags),
					tslog.Uint("nlmsg_seq", nlh.Seq),
					tslog.Uint("nlmsg_pid", nlh.Pid),
				)
			}
		}

		b = b[msgSize:]
	}

	return
}

func (c *conn) handleNleAttrs(b []byte) (msg string) {
	for len(b) >= unix.SizeofNlAttr {
		nla := (*unix.NlAttr)(unsafe.Pointer(unsafe.SliceData(b)))
		nlaSize := rtnetlink.NlaAlign(nla.Len)
		if nla.Len < unix.SizeofNlAttr || int(nlaSize) > len(b) {
			c.logger.Error("Invalid netlink attribute length",
				tslog.Uint("nla_len", nla.Len),
				tslog.Uint("nla_type", nla.Type),
				tslog.Uint("nlaSize", nlaSize),
				tslog.Int("len(b)", len(b)),
			)
			return
		}

		switch nla.Type {
		case unix.NLMSGERR_ATTR_MSG:
			if nla.Len < unix.SizeofNlAttr+1 {
				c.logger.Error("Invalid NLMSGERR_ATTR_MSG attribute length",
					tslog.Uint("nla_len", nla.Len),
					tslog.Uint("nla_type", nla.Type),
				)
				return
			}

			msgBuf := b[unix.SizeofNlAttr : nla.Len-1] // -1 to exclude the null terminator
			msg = unsafe.String(unsafe.SliceData(msgBuf), len(msgBuf))

			if c.logger.Enabled(slog.LevelDebug) {
				c.logger.Debug("Parsed NLMSGERR_ATTR_MSG attribute", slog.String("msg", msg))
			}

		default:
			if c.logger.Enabled(slog.LevelDebug) {
				c.logger.Debug("Skipping netlink attribute",
					tslog.Uint("nla_len", nla.Len),
					tslog.Uint("nla_type", nla.Type),
				)
			}
		}

		b = b[nlaSize:]
	}

	return
}

func (c *conn) handleIfiAttrs(b []byte) (name string) {
	for len(b) >= unix.SizeofRtAttr {
		rta := (*unix.RtAttr)(unsafe.Pointer(unsafe.SliceData(b)))
		rtaSize := rtnetlink.RtaAlign(rta.Len)
		if rta.Len < unix.SizeofRtAttr || int(rtaSize) > len(b) {
			c.logger.Error("Invalid rtnetlink attribute length",
				tslog.Uint("rta_len", rta.Len),
				tslog.Uint("rta_type", rta.Type),
				tslog.Uint("rtaSize", rtaSize),
				tslog.Int("len(b)", len(b)),
			)
			return
		}

		switch rta.Type {
		case unix.IFLA_IFNAME:
			if rta.Len < unix.SizeofRtAttr+1 {
				c.logger.Error("Invalid IFLA_IFNAME attribute length",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
				)
				return
			}

			nameBuf := b[unix.SizeofRtAttr : rta.Len-1] // -1 to exclude the null terminator
			name = unsafe.String(unsafe.SliceData(nameBuf), len(nameBuf))

		default:
			if c.logger.Enabled(slog.LevelDebug) {
				c.logger.Debug("Skipping rtnetlink attribute",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
				)
			}
		}

		b = b[rtaSize:]
	}

	return
}

func (c *conn) handleIfaAttrs(b []byte) (addr netip.Addr, label string, cacheInfo unix.IfaCacheinfo, flags uint32) {
	for len(b) >= unix.SizeofRtAttr {
		rta := (*unix.RtAttr)(unsafe.Pointer(unsafe.SliceData(b)))
		rtaSize := rtnetlink.RtaAlign(rta.Len)
		if rta.Len < unix.SizeofRtAttr || int(rtaSize) > len(b) {
			c.logger.Error("Invalid rtnetlink attribute length",
				tslog.Uint("rta_len", rta.Len),
				tslog.Uint("rta_type", rta.Type),
				tslog.Uint("rtaSize", rtaSize),
				tslog.Int("len(b)", len(b)),
			)
			return
		}

		switch rta.Type {
		case unix.IFA_ADDRESS:
			addrLen := int(rta.Len) - unix.SizeofRtAttr
			switch addrLen {
			case 4:
				addr = netip.AddrFrom4([4]byte(b[unix.SizeofRtAttr:rta.Len]))
			case 16:
				addr = netip.AddrFrom16([16]byte(b[unix.SizeofRtAttr:rta.Len]))
			default:
				c.logger.Error("Invalid IFA_ADDRESS attribute length",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
					tslog.Int("addrLen", addrLen),
				)
				return
			}

		case unix.IFA_LABEL:
			if rta.Len < unix.SizeofRtAttr+1 {
				c.logger.Error("Invalid IFA_LABEL attribute length",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
				)
				return
			}

			labelBuf := b[unix.SizeofRtAttr : rta.Len-1] // -1 to exclude the null terminator
			label = unsafe.String(unsafe.SliceData(labelBuf), len(labelBuf))

		case unix.IFA_CACHEINFO:
			if rta.Len < unix.SizeofRtAttr+unix.SizeofIfaCacheinfo {
				c.logger.Error("Invalid IFA_CACHEINFO attribute length",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
				)
				return
			}

			cacheInfo = *(*unix.IfaCacheinfo)(unsafe.Pointer(unsafe.SliceData(b[unix.SizeofRtAttr:])))

		case unix.IFA_FLAGS:
			if rta.Len < unix.SizeofRtAttr+4 {
				c.logger.Error("Invalid IFA_FLAGS attribute length",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
				)
				return
			}

			flags = *(*uint32)(unsafe.Pointer(unsafe.SliceData(b[unix.SizeofRtAttr:])))

		default:
			if c.logger.Enabled(slog.LevelDebug) {
				c.logger.Debug("Skipping rtnetlink attribute",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
				)
			}
		}

		b = b[rtaSize:]
	}

	return
}

func (p *producer) handleStateSyncs() {
	for {
		if err := p.getLinkDump(); err != nil {
			p.logger.Error("Failed to get RTM_GETLINK dump", tslog.Err(err))
		}

		if err := p.getAddrDump(); err != nil {
			p.logger.Error("Failed to get RTM_GETADDR dump", tslog.Err(err))
		}

		if _, ok := <-p.resyncCh; !ok {
			return
		}
	}
}

type addrUpdateKind uint8

const (
	addrUpdateKindAdd = iota
	addrUpdateKindDelete
)

type addrUpdate struct {
	addr netip.Addr
	kind addrUpdateKind
}

type ruleManager struct {
	updateCh chan addrUpdate
	conn
}

func (m *ruleManager) NotifyAddAddr(addr netip.Addr) {
	m.updateCh <- addrUpdate{addr: addr, kind: addrUpdateKindAdd}
}

func (m *ruleManager) NotifyDelAddr(addr netip.Addr) {
	m.updateCh <- addrUpdate{addr: addr, kind: addrUpdateKindDelete}
}

func (m *ruleManager) Stop() {
	close(m.updateCh) // unblock handleAddrUpdates
	_ = m.closeNlConn()
}

func (m *ruleManager) Start(ctx context.Context, wg *sync.WaitGroup) error {
	if err := m.openNlConn(0); err != nil {
		return fmt.Errorf("failed to open netlink connection: %w", err)
	}

	_ = context.AfterFunc(ctx, func() {
		if err := m.nlConn.SetReadDeadline(aLongTimeAgo); err != nil {
			m.logger.Error("Failed to set read deadline on netlink connection", tslog.Err(err))
		}
	})

	wg.Go(func() {
		m.readResponses()
		close(m.respCh) // unblock sendAndWait and thus handleAddrUpdates
	})

	wg.Go(m.handleAddrUpdates)
	return nil
}

func (m *ruleManager) readResponses() {
	var rsa unix.RawSockaddrNetlink
	rb := make([]byte, readBufSize)
	riov := unix.Iovec{
		Base: unsafe.SliceData(rb),
		Len:  readBufSize,
	}
	rmsg := unix.Msghdr{
		Name:    (*byte)(unsafe.Pointer(&rsa)),
		Namelen: unix.SizeofSockaddrNetlink,
		Iov:     &riov,
		Iovlen:  1,
	}
	rc := m.nlConn.NewRConn()

	for {
		n, err := rc.ReadMsg(&rmsg, 0)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				break
			}
			m.logger.Error("Failed to read netlink message", tslog.Err(err))
			continue
		}

		if m.logger.Enabled(slog.LevelDebug) {
			m.logger.Debug("Received netlink message",
				tslog.Int("n", n),
				tslog.Uint("nl_pid", rsa.Pid),
				tslog.Uint("nl_groups", rsa.Groups),
			)
		}

		m.handleNetlinkMessage(rb[:n])
	}
}

func (m *ruleManager) handleNetlinkMessage(b []byte) {
	for len(b) >= unix.SizeofNlMsghdr {
		nlh := (*unix.NlMsghdr)(unsafe.Pointer(unsafe.SliceData(b)))
		msgSize := rtnetlink.NlMsgAlign(nlh.Len)
		if nlh.Len < unix.SizeofNlMsghdr || int(msgSize) > len(b) {
			m.logger.Error("Invalid netlink message length",
				tslog.Uint("nlmsg_len", nlh.Len),
				tslog.Uint("nlmsg_type", nlh.Type),
				tslog.Uint("nlmsg_flags", nlh.Flags),
				tslog.Uint("nlmsg_seq", nlh.Seq),
				tslog.Uint("nlmsg_pid", nlh.Pid),
				tslog.Uint("msgSize", msgSize),
				tslog.Int("len(b)", len(b)),
			)
			return
		}

		switch nlh.Type {
		case unix.NLMSG_ERROR:
			if nlh.Len < unix.SizeofNlMsghdr+unix.SizeofNlMsgerr {
				m.logger.Error("Invalid NLMSG_ERROR message length",
					tslog.Uint("nlmsg_len", nlh.Len),
					tslog.Uint("nlmsg_type", nlh.Type),
					tslog.Uint("nlmsg_flags", nlh.Flags),
					tslog.Uint("nlmsg_seq", nlh.Seq),
					tslog.Uint("nlmsg_pid", nlh.Pid),
				)
				return
			}

			nle := (*unix.NlMsgerr)(unsafe.Pointer(unsafe.SliceData(b[unix.SizeofNlMsghdr:])))
			msg := m.handleNleAttrs(b[unix.SizeofNlMsghdr+unix.SizeofNlMsgerr : nlh.Len])
			lvl := slog.LevelDebug
			if nle.Error != 0 {
				lvl = slog.LevelError
			}

			if m.logger.Enabled(lvl) {
				m.logger.Log(lvl, "Received response to netlink request",
					tslog.Int("error", nle.Error),
					tslog.Uint("seq", nle.Msg.Seq),
					slog.String("msg", msg),
				)
			}

			m.respCh <- response{seq: nle.Msg.Seq, err: nle.Error}

		default:
			if m.logger.Enabled(slog.LevelDebug) {
				m.logger.Debug("Skipping netlink message",
					tslog.Uint("nlmsg_len", nlh.Len),
					tslog.Uint("nlmsg_type", nlh.Type),
					tslog.Uint("nlmsg_flags", nlh.Flags),
					tslog.Uint("nlmsg_seq", nlh.Seq),
					tslog.Uint("nlmsg_pid", nlh.Pid),
				)
			}
		}

		b = b[msgSize:]
	}
}

func (m *ruleManager) handleAddrUpdates() {
	for update := range m.updateCh {
		switch update.kind {
		case addrUpdateKindAdd:
			if err := m.addRule(update.addr); err != nil {
				m.logger.Error("Failed to add policy routing rule",
					tslog.Addr("addr", update.addr),
					tslog.Err(err),
				)
				continue
			}
			m.logger.Info("Added policy routing rule", tslog.Addr("addr", update.addr))

		case addrUpdateKindDelete:
			if err := m.delRule(update.addr); err != nil {
				m.logger.Error("Failed to delete policy routing rule",
					tslog.Addr("addr", update.addr),
					tslog.Err(err),
				)
				continue
			}
			m.logger.Info("Deleted policy routing rule", tslog.Addr("addr", update.addr))

		default:
			panic("unreachable")
		}
	}
}

func (c *conn) sendAndWait(nlh *unix.NlMsghdr) error {
	c.seq++
	nlh.Seq = c.seq
	b := unsafe.Slice((*byte)(unsafe.Pointer(nlh)), nlh.Len)
	if _, err := c.nlConn.Write(b); err != nil {
		return err
	}

	resp, ok := <-c.respCh
	if !ok {
		return nil
	}
	if resp.seq != nlh.Seq {
		c.logger.Error("Received response with unexpected sequence number",
			tslog.Uint("reqSeq", nlh.Seq),
			tslog.Uint("respSeq", resp.seq),
			tslog.Int("respErr", resp.err),
		)
		return nil
	}
	if resp.err != 0 {
		return syscall.Errno(-resp.err)
	}
	return nil
}

type linkRequest struct {
	Header  unix.NlMsghdr
	Message unix.IfInfomsg
}

func (c *conn) getLinkDump() error {
	const msgLen = unix.SizeofNlMsghdr + unix.SizeofIfInfomsg
	req := linkRequest{
		Header: unix.NlMsghdr{
			Len:   msgLen,
			Type:  unix.RTM_GETLINK,
			Flags: unix.NLM_F_REQUEST | unix.NLM_F_ACK | unix.NLM_F_DUMP,
		},
		Message: unix.IfInfomsg{
			Family: unix.AF_UNSPEC,
			Type:   unix.ARPHRD_NETROM,
		},
	}
	return c.sendAndWait(&req.Header)
}

type addrRequest struct {
	Header  unix.NlMsghdr
	Message unix.IfAddrmsg
}

func (c *conn) getAddrDump() error {
	const msgLen = unix.SizeofNlMsghdr + unix.SizeofIfAddrmsg
	req := addrRequest{
		Header: unix.NlMsghdr{
			Len:   msgLen,
			Type:  unix.RTM_GETADDR,
			Flags: unix.NLM_F_REQUEST | unix.NLM_F_ACK | unix.NLM_F_DUMP,
		},
		Message: unix.IfAddrmsg{
			Family: unix.AF_UNSPEC,
			Scope:  unix.RT_SCOPE_UNIVERSE,
		},
	}
	return c.sendAndWait(&req.Header)
}

type ruleRequest struct {
	Header       unix.NlMsghdr
	Message      unix.RtMsg
	FraSrcHeader unix.RtAttr
	FraSrcAddr   [16]byte
}

func putRuleRequest(req *ruleRequest, addr netip.Addr, msgType, flags uint16, action uint8) {
	var (
		family        uint8
		addrBitlen    uint8
		fraSrcAttrLen uint16
		msgLen        uint32
		addrBytes     [16]byte
	)

	if addr.Is4() {
		family = unix.AF_INET
		addrBitlen = 32
		fraSrcAttrLen = unix.SizeofRtAttr + 4
		msgLen = unix.SizeofNlMsghdr + unix.SizeofRtMsg + unix.SizeofRtAttr + 4
		*(*[4]byte)(addrBytes[:]) = addr.As4()
	} else {
		family = unix.AF_INET6
		addrBitlen = 128
		fraSrcAttrLen = unix.SizeofRtAttr + 16
		msgLen = unix.SizeofNlMsghdr + unix.SizeofRtMsg + unix.SizeofRtAttr + 16
		addrBytes = addr.As16()
	}

	*req = ruleRequest{
		Header: unix.NlMsghdr{
			Len:   msgLen,
			Type:  msgType,
			Flags: flags,
		},
		Message: unix.RtMsg{
			Family:  family,
			Src_len: addrBitlen,
			Table:   unix.RT_TABLE_MAIN,
			Type:    action, // action in fib_rule_hdr
		},
		FraSrcHeader: unix.RtAttr{
			Len:  fraSrcAttrLen,
			Type: unix.FRA_SRC,
		},
		FraSrcAddr: addrBytes,
	}
}

func (c *conn) addRule(addr netip.Addr) error {
	var req ruleRequest
	putRuleRequest(
		&req,
		addr,
		unix.RTM_NEWRULE,
		// NLM_F_EXCL and NLM_F_CREATE don't seem to have any effect here.
		// But we specify them anyway to be consistent with iproute2.
		unix.NLM_F_REQUEST|unix.NLM_F_ACK|unix.NLM_F_EXCL|unix.NLM_F_CREATE,
		unix.FR_ACT_TO_TBL,
	)
	return c.sendAndWait(&req.Header)
}

func (c *conn) delRule(addr netip.Addr) error {
	var req ruleRequest
	putRuleRequest(
		&req,
		addr,
		unix.RTM_DELRULE,
		unix.NLM_F_REQUEST|unix.NLM_F_ACK,
		unix.FR_ACT_UNSPEC,
	)
	return c.sendAndWait(&req.Header)
}
