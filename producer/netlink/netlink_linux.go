package netlink

import (
	"context"
	"errors"
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
	return &Producer{
		producer: producer{
			logger:             logger,
			broadcaster:        broadcaster.New(),
			respChBySeq:        make(map[uint32]chan<- syscall.Errno),
			ifname:             cfg.Interface,
			fromAddrLookupMain: cfg.FromAddrLookupMain,
		},
	}, nil
}

type producer struct {
	logger             *tslog.Logger
	broadcaster        *broadcaster.Broadcaster
	respChBySeq        map[uint32]chan<- syscall.Errno
	ifname             string
	fromAddrLookupMain bool
	seq                uint32
	ifindex            uint32
	addr4              netip.Addr
	addr6              netip.Addr
	addr4valid         uint32
	addr6valid         uint32
}

func (p *producer) subscribe() <-chan producerpkg.Message {
	return p.broadcaster.Subscribe()
}

// aLongTimeAgo is a non-zero time, far in the past, used for immediate deadlines.
var aLongTimeAgo = time.Unix(0, 0)

func (p *producer) run(ctx context.Context) {
	c, err := rtnetlink.Open(unix.RTMGRP_LINK | unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR)
	if err != nil {
		p.logger.Error("Failed to open netlink connection", tslog.Err(err))
		return
	}
	defer c.Close()

	p.logger.Info("Started monitoring network interface", slog.String("interface", p.ifname))

	done := ctx.Done()
	go func() {
		<-done
		// We MUST NOT set a write deadline, as it will cause the rule removal on exit to fail.
		if err := c.SetReadDeadline(aLongTimeAgo); err != nil {
			p.logger.Error("Failed to set deadline on netlink connection", tslog.Err(err))
		}
	}()

	var ruleAddrUpdateCh chan addrUpdate
	if p.fromAddrLookupMain {
		ruleAddrUpdateCh = make(chan addrUpdate, 1)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.readAndHandle(c.NewRConn(), ruleAddrUpdateCh)
	}()

	wc := c.NewWConn()
	wsa := unix.RawSockaddrNetlink{
		Family: unix.AF_NETLINK,
	}
	const writeBufSize = max(
		unsafe.Sizeof(linkRequest{}),
		unsafe.Sizeof(addrRequest{}),
		unsafe.Sizeof(ruleRequest{}),
	)
	wb := make([]byte, writeBufSize)
	wiov := unix.Iovec{
		Base: unsafe.SliceData(wb),
	}
	wmsg := unix.Msghdr{
		Name:    (*byte)(unsafe.Pointer(&wsa)),
		Namelen: unix.SizeofSockaddrNetlink,
		Iov:     &wiov,
		Iovlen:  1,
	}

	if err = p.getLinkDump(done, wc, &wmsg, wb); err != nil {
		p.logger.Error("Failed to get RTM_GETLINK dump", tslog.Err(err))
	}

	if err = p.getAddrDump(done, wc, &wmsg, wb); err != nil {
		p.logger.Error("Failed to get RTM_GETADDR dump", tslog.Err(err))
	}

	if ruleAddrUpdateCh != nil {
		p.handleRuleUpdates(done, ruleAddrUpdateCh, wc, &wmsg, wb)
	}

	wg.Wait()

	p.logger.Info("Stopped monitoring network interface", slog.String("interface", p.ifname))
}

type addrUpdate struct {
	addr4        netip.Addr
	addr6        netip.Addr
	addr4updated bool
	addr6updated bool
}

func (p *producer) readAndHandle(rc *rtnetlink.RConn, ruleAddrUpdateCh chan<- addrUpdate) {
	if ruleAddrUpdateCh != nil {
		defer close(ruleAddrUpdateCh)
	}

	var rsa unix.RawSockaddrNetlink
	const readBufSize = 32 * 1024
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

	for {
		n, err := rc.ReadMsg(&rmsg, 0)
		if err != nil {
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
				slog.String("v4", p.addr4.String()),
				slog.String("v6", p.addr6.String()),
			)
		}

		p.broadcaster.Broadcast(producerpkg.Message{
			IPv4: p.addr4,
			IPv6: p.addr6,
		})

		if ruleAddrUpdateCh != nil {
			ruleAddrUpdateCh <- addrUpdate{
				addr4:        p.addr4,
				addr6:        p.addr6,
				addr4updated: addr4updated,
				addr6updated: addr6updated,
			}
		}
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
			p.sendRespBySeq(nlh.Seq, 0)
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

			p.sendRespBySeq(nle.Msg.Seq, syscall.Errno(-nle.Error))

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
						slog.String("addr", addr.String()),
						slog.String("label", label),
						tslog.Uint("ifa_prefered", cacheInfo.Prefered),
						tslog.Uint("ifa_valid", cacheInfo.Valid),
						tslog.Uint("cstamp", cacheInfo.Cstamp),
						tslog.Uint("tstamp", cacheInfo.Tstamp),
						tslog.Uint("flags", flags),
					)
				}

				// Skip temporary and deprecated addresses.
				if flags&unix.IFA_F_TEMPORARY != 0 || flags&unix.IFA_F_DEPRECATED != 0 {
					break
				}

				// Skip link-local addresses.
				if addr.IsLinkLocalUnicast() {
					break
				}

				switch ifam.Family {
				case unix.AF_INET:
					if !addr.Is4() {
						p.logger.Error("Invalid IPv4 address",
							slog.String("addr", addr.String()),
						)
						break
					}

					switch nlh.Type {
					case unix.RTM_NEWADDR:
						switch {
						case p.addr4 == addr:
							if p.logger.Enabled(slog.LevelDebug) {
								p.logger.Debug("Updating cached IPv4 address valid time",
									slog.String("addr", addr.String()),
									tslog.Uint("oldValid", p.addr4valid),
									tslog.Uint("newValid", cacheInfo.Valid),
								)
							}
							p.addr4valid = cacheInfo.Valid

						case p.addr4valid < cacheInfo.Valid:
							if p.logger.Enabled(slog.LevelDebug) {
								p.logger.Debug("Updating cached IPv4 address",
									slog.String("oldAddr", p.addr4.String()),
									tslog.Uint("oldValid", p.addr4valid),
									slog.String("newAddr", addr.String()),
									tslog.Uint("newValid", cacheInfo.Valid),
								)
							}
							p.addr4 = addr
							p.addr4valid = cacheInfo.Valid
							addr4updated = true
						}

					case unix.RTM_DELADDR:
						if p.addr4 == addr {
							if p.logger.Enabled(slog.LevelDebug) {
								p.logger.Debug("Removing cached IPv4 address",
									slog.String("addr", addr.String()),
									tslog.Uint("valid", cacheInfo.Valid),
								)
							}
							p.addr4 = netip.Addr{}
							p.addr4valid = 0
							addr4updated = true
						}
					}

				case unix.AF_INET6:
					if !addr.Is6() {
						p.logger.Error("Invalid IPv6 address",
							slog.String("addr", addr.String()),
						)
						break
					}

					switch nlh.Type {
					case unix.RTM_NEWADDR:
						switch {
						case p.addr6 == addr:
							if p.logger.Enabled(slog.LevelDebug) {
								p.logger.Debug("Updating cached IPv6 address valid time",
									slog.String("addr", addr.String()),
									tslog.Uint("oldValid", p.addr6valid),
									tslog.Uint("newValid", cacheInfo.Valid),
								)
							}
							p.addr6valid = cacheInfo.Valid

						case p.addr6valid < cacheInfo.Valid:
							if p.logger.Enabled(slog.LevelDebug) {
								p.logger.Debug("Updating cached IPv6 address",
									slog.String("oldAddr", p.addr6.String()),
									tslog.Uint("oldValid", p.addr6valid),
									slog.String("newAddr", addr.String()),
									tslog.Uint("newValid", cacheInfo.Valid),
								)
							}
							p.addr6 = addr
							p.addr6valid = cacheInfo.Valid
							addr6updated = true
						}

					case unix.RTM_DELADDR:
						if p.addr6 == addr {
							if p.logger.Enabled(slog.LevelDebug) {
								p.logger.Debug("Removing cached IPv6 address",
									slog.String("addr", addr.String()),
									tslog.Uint("valid", cacheInfo.Valid),
								)
							}
							p.addr6 = netip.Addr{}
							p.addr6valid = 0
							addr6updated = true
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

func (p *producer) handleNleAttrs(b []byte) (msg string) {
	for len(b) >= unix.SizeofNlAttr {
		nla := (*unix.NlAttr)(unsafe.Pointer(unsafe.SliceData(b)))
		nlaSize := rtnetlink.NlaAlign(nla.Len)
		if nla.Len < unix.SizeofNlAttr || int(nlaSize) > len(b) {
			p.logger.Error("Invalid netlink attribute length",
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
				p.logger.Error("Invalid NLMSGERR_ATTR_MSG attribute length",
					tslog.Uint("nla_len", nla.Len),
					tslog.Uint("nla_type", nla.Type),
				)
				return
			}

			msgBuf := b[unix.SizeofNlAttr : nla.Len-1] // -1 to exclude the null terminator
			msg = unsafe.String(unsafe.SliceData(msgBuf), len(msgBuf))

			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Parsed NLMSGERR_ATTR_MSG attribute", slog.String("msg", msg))
			}

		default:
			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Skipping netlink attribute",
					tslog.Uint("nla_len", nla.Len),
					tslog.Uint("nla_type", nla.Type),
				)
			}
		}

		b = b[nlaSize:]
	}

	return
}

func (p *producer) handleIfiAttrs(b []byte) (name string) {
	for len(b) >= unix.SizeofRtAttr {
		rta := (*unix.RtAttr)(unsafe.Pointer(unsafe.SliceData(b)))
		rtaSize := rtnetlink.RtaAlign(rta.Len)
		if rta.Len < unix.SizeofRtAttr || int(rtaSize) > len(b) {
			p.logger.Error("Invalid rtnetlink attribute length",
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
				p.logger.Error("Invalid IFLA_IFNAME attribute length",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
				)
				return
			}

			nameBuf := b[unix.SizeofRtAttr : rta.Len-1] // -1 to exclude the null terminator
			name = unsafe.String(unsafe.SliceData(nameBuf), len(nameBuf))

		default:
			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Skipping rtnetlink attribute",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
				)
			}
		}

		b = b[rtaSize:]
	}

	return
}

func (p *producer) handleIfaAttrs(b []byte) (addr netip.Addr, label string, cacheInfo unix.IfaCacheinfo, flags uint32) {
	for len(b) >= unix.SizeofRtAttr {
		rta := (*unix.RtAttr)(unsafe.Pointer(unsafe.SliceData(b)))
		rtaSize := rtnetlink.RtaAlign(rta.Len)
		if rta.Len < unix.SizeofRtAttr || int(rtaSize) > len(b) {
			p.logger.Error("Invalid rtnetlink attribute length",
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
				p.logger.Error("Invalid IFA_ADDRESS attribute length",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
					tslog.Int("addrLen", addrLen),
				)
				return
			}

		case unix.IFA_LABEL:
			if rta.Len < unix.SizeofRtAttr+1 {
				p.logger.Error("Invalid IFA_LABEL attribute length",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
				)
				return
			}

			labelBuf := b[unix.SizeofRtAttr : rta.Len-1] // -1 to exclude the null terminator
			label = unsafe.String(unsafe.SliceData(labelBuf), len(labelBuf))

		case unix.IFA_CACHEINFO:
			if rta.Len < unix.SizeofRtAttr+unix.SizeofIfaCacheinfo {
				p.logger.Error("Invalid IFA_CACHEINFO attribute length",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
				)
				return
			}

			cacheInfo = *(*unix.IfaCacheinfo)(unsafe.Pointer(unsafe.SliceData(b[unix.SizeofRtAttr:])))

		case unix.IFA_FLAGS:
			if rta.Len < unix.SizeofRtAttr+4 {
				p.logger.Error("Invalid IFA_FLAGS attribute length",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
				)
				return
			}

			flags = *(*uint32)(unsafe.Pointer(unsafe.SliceData(b[unix.SizeofRtAttr:])))

		default:
			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Skipping rtnetlink attribute",
					tslog.Uint("rta_len", rta.Len),
					tslog.Uint("rta_type", rta.Type),
				)
			}
		}

		b = b[rtaSize:]
	}

	return
}

func (p *producer) handleRuleUpdates(done <-chan struct{}, ruleAddrUpdateCh <-chan addrUpdate, wc *rtnetlink.WConn, msg *unix.Msghdr, b []byte) {
	var (
		fromAddr4 netip.Addr
		fromAddr6 netip.Addr
	)

	for update := range ruleAddrUpdateCh {
		if update.addr4updated {
			// Delete the old rule if it exists.
			if err := p.delRuleIfAddrValid(done, fromAddr4, wc, msg, b); err != nil {
				p.logger.Error("Failed to delete old IPv4 rule",
					slog.String("addr", fromAddr4.String()),
					tslog.Err(err),
				)
			}

			// Add a new rule if there's a new address.
			if err := p.addRuleIfAddrValid(done, update.addr4, wc, msg, b); err != nil {
				p.logger.Error("Failed to add new IPv4 rule",
					slog.String("addr", update.addr4.String()),
					tslog.Err(err),
				)
			}

			fromAddr4 = update.addr4
		}

		if update.addr6updated {
			// Delete the old rule if it exists.
			if err := p.delRuleIfAddrValid(done, fromAddr6, wc, msg, b); err != nil {
				p.logger.Error("Failed to delete old IPv6 rule",
					slog.String("addr", fromAddr6.String()),
					tslog.Err(err),
				)
			}

			// Add a new rule if there's a new address.
			if err := p.addRuleIfAddrValid(done, update.addr6, wc, msg, b); err != nil {
				p.logger.Error("Failed to add new IPv6 rule",
					slog.String("addr", update.addr6.String()),
					tslog.Err(err),
				)
			}

			fromAddr6 = update.addr6
		}
	}

	// Delete the rules on exit.
	if err := p.delRuleIfAddrValid(done, fromAddr4, wc, msg, b); err != nil {
		p.logger.Error("Failed to delete IPv4 rule on exit",
			slog.String("addr", fromAddr4.String()),
			tslog.Err(err),
		)
	}

	if err := p.delRuleIfAddrValid(done, fromAddr6, wc, msg, b); err != nil {
		p.logger.Error("Failed to delete IPv6 rule on exit",
			slog.String("addr", fromAddr6.String()),
			tslog.Err(err),
		)
	}
}

func (p *producer) newSeq() (uint32, <-chan syscall.Errno) {
	p.seq++
	respCh := make(chan syscall.Errno, 1)
	p.respChBySeq[p.seq] = respCh
	return p.seq, respCh
}

func (p *producer) sendRespBySeq(seq uint32, errno syscall.Errno) {
	respCh, ok := p.respChBySeq[seq]
	if ok {
		respCh <- errno
		delete(p.respChBySeq, seq)
	}
}

func (p *producer) sendAndWait(
	done <-chan struct{},
	wc *rtnetlink.WConn,
	msg *unix.Msghdr,
	b []byte,
	putRequest func([]byte, uint32, *unix.Msghdr),
) error {
	seq, respCh := p.newSeq()
	putRequest(b, seq, msg)
	if _, err := wc.WriteMsg(msg, 0); err != nil {
		return err
	}

	select {
	case <-done:
		return nil
	case errno := <-respCh:
		if errno != 0 {
			return errno
		}
		return nil
	}
}

type linkRequest struct {
	Header  unix.NlMsghdr
	Message unix.IfInfomsg
}

func putLinkRequest(b []byte, seq uint32, msg *unix.Msghdr) {
	const msgLen = unix.SizeofNlMsghdr + unix.SizeofIfInfomsg
	reqBuf := b[:msgLen]
	req := (*linkRequest)(unsafe.Pointer(unsafe.SliceData(reqBuf)))
	*req = linkRequest{
		Header: unix.NlMsghdr{
			Len:   msgLen,
			Type:  unix.RTM_GETLINK,
			Flags: unix.NLM_F_REQUEST | unix.NLM_F_ACK | unix.NLM_F_DUMP,
			Seq:   seq,
		},
		Message: unix.IfInfomsg{
			Family: unix.AF_UNSPEC,
			Type:   unix.ARPHRD_NETROM,
		},
	}
	msg.Iov.Len = msgLen
}

func (p *producer) getLinkDump(done <-chan struct{}, wc *rtnetlink.WConn, msg *unix.Msghdr, b []byte) error {
	return p.sendAndWait(done, wc, msg, b, putLinkRequest)
}

type addrRequest struct {
	Header  unix.NlMsghdr
	Message unix.IfAddrmsg
}

func putAddrRequest(b []byte, seq uint32, msg *unix.Msghdr) {
	const msgLen = unix.SizeofNlMsghdr + unix.SizeofIfAddrmsg
	reqBuf := b[:msgLen]
	req := (*addrRequest)(unsafe.Pointer(unsafe.SliceData(reqBuf)))
	*req = addrRequest{
		Header: unix.NlMsghdr{
			Len:   msgLen,
			Type:  unix.RTM_GETADDR,
			Flags: unix.NLM_F_REQUEST | unix.NLM_F_ACK | unix.NLM_F_DUMP,
			Seq:   seq,
		},
		Message: unix.IfAddrmsg{
			Family: unix.AF_UNSPEC,
			Scope:  unix.RT_SCOPE_UNIVERSE,
		},
	}
	msg.Iov.Len = msgLen
}

func (p *producer) getAddrDump(done <-chan struct{}, wc *rtnetlink.WConn, msg *unix.Msghdr, b []byte) error {
	return p.sendAndWait(done, wc, msg, b, putAddrRequest)
}

type ruleRequest struct {
	Header       unix.NlMsghdr
	Message      unix.RtMsg
	FraSrcHeader unix.RtAttr
	FraSrcAddr   [16]byte
}

func putRuleRequest(addr netip.Addr, b []byte, msgType, flags uint16, seq uint32, action uint8, msg *unix.Msghdr) {
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

	reqBuf := b[:unix.SizeofNlMsghdr+unix.SizeofRtMsg+unix.SizeofRtAttr+16]
	req := (*ruleRequest)(unsafe.Pointer(unsafe.SliceData(reqBuf)))
	*req = ruleRequest{
		Header: unix.NlMsghdr{
			Len:   msgLen,
			Type:  msgType,
			Flags: flags,
			Seq:   seq,
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
	msg.Iov.Len = uint64(msgLen)
}

func putAddRuleRequest(addr netip.Addr, b []byte, seq uint32, msg *unix.Msghdr) {
	putRuleRequest(
		addr,
		b,
		unix.RTM_NEWRULE,
		// NLM_F_EXCL and NLM_F_CREATE don't seem to have any effect here.
		// But we specify them anyway to be consistent with iproute2.
		unix.NLM_F_REQUEST|unix.NLM_F_ACK|unix.NLM_F_EXCL|unix.NLM_F_CREATE,
		seq,
		unix.FR_ACT_TO_TBL,
		msg,
	)
}

func putDelRuleRequest(addr netip.Addr, b []byte, seq uint32, msg *unix.Msghdr) {
	putRuleRequest(
		addr,
		b,
		unix.RTM_DELRULE,
		unix.NLM_F_REQUEST|unix.NLM_F_ACK,
		seq,
		unix.FR_ACT_UNSPEC,
		msg,
	)
}

func (p *producer) addRule(done <-chan struct{}, addr netip.Addr, wc *rtnetlink.WConn, msg *unix.Msghdr, b []byte) error {
	return p.sendAndWait(done, wc, msg, b, func(b []byte, seq uint32, msg *unix.Msghdr) {
		putAddRuleRequest(addr, b, seq, msg)
	})
}

func (p *producer) delRule(done <-chan struct{}, addr netip.Addr, wc *rtnetlink.WConn, msg *unix.Msghdr, b []byte) error {
	return p.sendAndWait(done, wc, msg, b, func(b []byte, seq uint32, msg *unix.Msghdr) {
		putDelRuleRequest(addr, b, seq, msg)
	})
}

func (p *producer) addRuleIfAddrValid(done <-chan struct{}, addr netip.Addr, wc *rtnetlink.WConn, msg *unix.Msghdr, b []byte) error {
	if addr.IsValid() {
		return p.addRule(done, addr, wc, msg, b)
	}
	return nil
}

func (p *producer) delRuleIfAddrValid(done <-chan struct{}, addr netip.Addr, wc *rtnetlink.WConn, msg *unix.Msghdr, b []byte) error {
	if addr.IsValid() {
		return p.delRule(done, addr, wc, msg, b)
	}
	return nil
}
