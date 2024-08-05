package win32iphlp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"slices"
	"sync"
	"syscall"
	"unsafe"

	producerpkg "github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/producer/internal/broadcaster"
	"github.com/database64128/ddns-go/producer/win32iphlp/internal/iphlpapi"
	"github.com/database64128/ddns-go/tslog"
	"golang.org/x/sys/windows"
)

type source struct {
	name              string
	buf               []byte
	luid              uint64
	irrelevantLuidSet map[uint64]struct{}
}

func newSource(name string) (*Source, error) {
	return &Source{
		source: source{
			name:              name,
			irrelevantLuidSet: make(map[uint64]struct{}),
		},
	}, nil
}

func (s *source) snapshot() (producerpkg.Message, error) {
	addr4, addr6, _, _, err := s.getAdaptersAddresses()
	return producerpkg.Message{
		IPv4: addr4,
		IPv6: addr6,
	}, err
}

func (s *source) getAdaptersAddresses() (addr4, addr6 netip.Addr, addr4ValidLifetime, addr6ValidLifetime uint32, err error) {
	const maxTries = 3
	size := uint32(max(15000, cap(s.buf))) // recommended initial size 15 KB
	for range maxTries {
		s.buf = slices.Grow(s.buf[:0], int(size))
		s.buf = s.buf[:size]
		p := (*windows.IpAdapterAddresses)(unsafe.Pointer(unsafe.SliceData(s.buf)))
		if err := windows.GetAdaptersAddresses(
			windows.AF_UNSPEC,
			windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER,
			0,
			p,
			&size,
		); err != nil {
			if err != windows.ERROR_BUFFER_OVERFLOW || size <= uint32(len(s.buf)) {
				return netip.Addr{}, netip.Addr{}, 0, 0, os.NewSyscallError("GetAdaptersAddresses", err)
			}
			continue
		}
		if size == 0 {
			p = nil
		}
		return s.parseAdapterAddresses(p)
	}
	return netip.Addr{}, netip.Addr{}, 0, 0, errors.New("ran out of tries for GetAdaptersAddresses")
}

func (s *source) parseAdapterAddresses(aa *windows.IpAdapterAddresses) (addr4, addr6 netip.Addr, addr4ValidLifetime, addr6ValidLifetime uint32, err error) {
	for ; aa != nil; aa = aa.Next {
		// Skip irrelevant interfaces.
		if s.luid != 0 {
			// We have the interface LUID, no need to compare the name.
			if s.luid != aa.Luid {
				continue
			}
		} else {
			// Check if the luid is in the irrelevant set.
			if _, ok := s.irrelevantLuidSet[aa.Luid]; ok {
				continue
			}

			// Not in the irrelevant set, check if the interface is up before comparing the name,
			// because at this time the name might be something generic like "Local Area Connection".
			if aa.OperStatus != windows.IfOperStatusUp {
				continue
			}

			if s.name != windows.UTF16PtrToString(aa.FriendlyName) {
				s.irrelevantLuidSet[aa.Luid] = struct{}{}
				continue
			}

			s.luid = aa.Luid
		}

		for ua := aa.FirstUnicastAddress; ua != nil; ua = ua.Next {
			// Skip temporary and deprecated addresses.
			if ua.SuffixOrigin == iphlpapi.IpSuffixOriginRandom ||
				ua.DadState == iphlpapi.IpDadStateDeprecated {
				continue
			}

			switch ua.Address.Sockaddr.Addr.Family {
			case windows.AF_INET:
				if ua.ValidLifetime <= addr4ValidLifetime {
					continue
				}
				addr4ValidLifetime = ua.ValidLifetime
				rsa := (*windows.RawSockaddrInet4)(unsafe.Pointer(ua.Address.Sockaddr))
				ip := netip.AddrFrom4(rsa.Addr)
				if ip.IsLinkLocalUnicast() {
					continue
				}
				addr4 = ip

			case windows.AF_INET6:
				if ua.ValidLifetime <= addr6ValidLifetime {
					continue
				}
				addr6ValidLifetime = ua.ValidLifetime
				rsa := (*windows.RawSockaddrInet6)(unsafe.Pointer(ua.Address.Sockaddr))
				ip := netip.AddrFrom16(rsa.Addr)
				if ip.IsLinkLocalUnicast() {
					continue
				}
				addr6 = ip
			}
		}

		return addr4, addr6, addr4ValidLifetime, addr6ValidLifetime, nil
	}

	return netip.Addr{}, netip.Addr{}, 0, 0, fmt.Errorf("no such network interface: %q", s.name)
}

func (cfg *ProducerConfig) newProducer(logger *tslog.Logger) (*Producer, error) {
	if cfg.Interface == "" {
		return nil, errors.New("interface name is required")
	}
	return &Producer{
		producer: producer{
			// It's been observed that NotifyIpInterfaceChange sends 2 initial notifications and
			// blocks until the callback calls return. Be safe here and give it 2 extra slots.
			notifyCh: make(chan mibNotification, 4),
			logger:   logger,
			source: source{
				name:              cfg.Interface,
				irrelevantLuidSet: make(map[uint64]struct{}),
			},
			broadcaster: broadcaster.New(),
		},
	}, nil
}

type mibNotification struct {
	address          windows.RawSockaddrInet6 // SOCKADDR_INET union
	interfaceLuid    uint64
	interfaceIndex   uint32
	notificationType uint32
}

type producer struct {
	notifyCh           chan mibNotification
	logger             *tslog.Logger
	addr4              netip.Addr
	addr6              netip.Addr
	addr4ValidLifetime uint32
	addr6ValidLifetime uint32
	source             source
	broadcaster        *broadcaster.Broadcaster
}

func (p *producer) subscribe() <-chan producerpkg.Message {
	return p.broadcaster.Subscribe()
}

var notifyUnicastIpAddressChangeCallback = sync.OnceValue(func() uintptr {
	return syscall.NewCallback(func(callerContext *chan<- mibNotification, row *iphlpapi.MibUnicastIpAddressRow, notificationType uint32) uintptr {
		notifyCh := *callerContext
		var nmsg mibNotification
		if row != nil {
			nmsg.address = row.Address
			nmsg.interfaceLuid = row.InterfaceLuid
			nmsg.interfaceIndex = row.InterfaceIndex
		}
		nmsg.notificationType = notificationType
		notifyCh <- nmsg
		return 0
	})
})

func (p *producer) run(ctx context.Context) {
	var notificationHandle windows.Handle

	if err := iphlpapi.NotifyUnicastIpAddressChange(
		windows.AF_UNSPEC,
		notifyUnicastIpAddressChangeCallback(),
		unsafe.Pointer(&p.notifyCh),
		true,
		&notificationHandle,
	); err != nil {
		p.logger.Error("Failed to register for IP address change notifications",
			tslog.Err(os.NewSyscallError("NotifyUnicastIpAddressChange", err)),
		)
		return
	}

	p.logger.Info("Registered for IP address change notifications",
		tslog.Uint("notificationHandle", notificationHandle),
	)

	defer func() {
		// Apparently, even on success, the notification handle can be NULL!
		// I mean, WTF, Microsoft?!
		if notificationHandle == 0 {
			p.logger.Debug("Skipping CancelMibChangeNotify2 because notification handle is NULL")
			return
		}
		if err := iphlpapi.CancelMibChangeNotify2(notificationHandle); err != nil {
			p.logger.Error("Failed to unregister for IP address change notifications",
				tslog.Uint("notificationHandle", notificationHandle),
				tslog.Err(os.NewSyscallError("CancelMibChangeNotify2", err)),
			)
			return
		}
		p.logger.Info("Unregistered for IP address change notifications")
	}()

	done := ctx.Done()
	for {
		select {
		case <-done:
			return
		case nmsg := <-p.notifyCh:
			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Received IP address change notification",
					tslog.Uint("luid", nmsg.interfaceLuid),
					tslog.Uint("index", nmsg.interfaceIndex),
					tslog.Uint("type", nmsg.notificationType),
				)
			}

			if updated := p.handleMibNotification(nmsg); !updated {
				continue
			}

			if p.logger.Enabled(slog.LevelInfo) {
				p.logger.Info("Broadcasting interface IP addresses",
					slog.Uint64("luid", p.source.luid),
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
}

func (p *producer) handleMibNotification(nmsg mibNotification) (updated bool) {
	switch nmsg.notificationType {
	case iphlpapi.MibParameterNotification, iphlpapi.MibAddInstance, iphlpapi.MibDeleteInstance:
		// Skip notifications for irrelevant interfaces.
		if p.source.luid != 0 {
			if p.source.luid != nmsg.interfaceLuid {
				if p.logger.Enabled(slog.LevelDebug) {
					p.logger.Debug("Skipping IP address change notification for different luid",
						tslog.Uint("luid", nmsg.interfaceLuid),
						tslog.Uint("index", nmsg.interfaceIndex),
						tslog.Uint("type", nmsg.notificationType),
					)
				}
				return false
			}
		} else {
			// Check if the luid is in the irrelevant set.
			if _, ok := p.source.irrelevantLuidSet[nmsg.interfaceLuid]; ok {
				if p.logger.Enabled(slog.LevelDebug) {
					p.logger.Debug("Skipping IP address change notification for irrelevant luid",
						tslog.Uint("luid", nmsg.interfaceLuid),
						tslog.Uint("index", nmsg.interfaceIndex),
						tslog.Uint("type", nmsg.notificationType),
					)
				}
				return false
			}

			// Unknown luid, retrieve interface informaton and compare the name.
			row := iphlpapi.MibIfRow2{
				InterfaceLuid: nmsg.interfaceLuid,
			}

			if err := iphlpapi.GetIfEntry2Ex(
				iphlpapi.MibIfEntryNormalWithoutStatistics,
				&row,
			); err != nil {
				p.logger.Warn("Failed to get interface information for IP address change notification",
					tslog.Uint("luid", nmsg.interfaceLuid),
					tslog.Uint("index", nmsg.interfaceIndex),
					tslog.Uint("type", nmsg.notificationType),
					tslog.Err(os.NewSyscallError("GetIfEntry2Ex", err)),
				)
				return false
			}

			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Retrieved interface information for IP address change notification",
					tslog.Uint("luid", nmsg.interfaceLuid),
					tslog.Uint("index", nmsg.interfaceIndex),
					tslog.Uint("type", nmsg.notificationType),
					tslog.Uint("operStatus", row.OperStatus),
					tslog.Uint("adminStatus", row.AdminStatus),
					tslog.Uint("mediaConnectState", row.MediaConnectState),
				)
			}

			// Skip name comparison if the interface is not up,
			// because at this time the name might be something generic like "Local Area Connection".
			if row.OperStatus != windows.IfOperStatusUp {
				if p.logger.Enabled(slog.LevelDebug) {
					p.logger.Debug("Skipping IP address change notification because interface is not up",
						tslog.Uint("luid", nmsg.interfaceLuid),
						tslog.Uint("index", nmsg.interfaceIndex),
						tslog.Uint("type", nmsg.notificationType),
						tslog.Uint("operStatus", row.OperStatus),
					)
				}
				return false
			}

			if name := windows.UTF16ToString(row.Alias[:]); name != p.source.name {
				if p.logger.Enabled(slog.LevelDebug) {
					p.logger.Debug("Skipping IP address change notification for interface with different name",
						slog.String("name", name),
						tslog.Uint("luid", nmsg.interfaceLuid),
						tslog.Uint("index", nmsg.interfaceIndex),
						tslog.Uint("type", nmsg.notificationType),
					)
				}
				p.source.irrelevantLuidSet[nmsg.interfaceLuid] = struct{}{}
				return false
			}

			if p.logger.Enabled(slog.LevelInfo) {
				p.logger.Info("Found interface",
					slog.String("name", p.source.name),
					tslog.Uint("luid", nmsg.interfaceLuid),
					tslog.Uint("index", nmsg.interfaceIndex),
				)
			}

			p.source.luid = nmsg.interfaceLuid
		}

		var addr netip.Addr
		switch nmsg.address.Family {
		case windows.AF_INET:
			rsa := (*windows.RawSockaddrInet4)(unsafe.Pointer(&nmsg.address))
			addr = netip.AddrFrom4(rsa.Addr)
		case windows.AF_INET6:
			addr = netip.AddrFrom16(nmsg.address.Addr)
		default:
			p.logger.Error("Unknown IP address family",
				tslog.Uint("family", nmsg.address.Family),
				tslog.Uint("luid", nmsg.interfaceLuid),
				tslog.Uint("index", nmsg.interfaceIndex),
				tslog.Uint("type", nmsg.notificationType),
			)
			return false
		}

		// Skip link-local addresses.
		if addr.IsLinkLocalUnicast() {
			return false
		}

		switch nmsg.notificationType {
		case iphlpapi.MibParameterNotification:
			if addr != p.addr4 && addr != p.addr6 {
				return false
			}

		case iphlpapi.MibDeleteInstance:
			switch addr {
			case p.addr4:
				if p.logger.Enabled(slog.LevelDebug) {
					p.logger.Debug("Removing cached IPv4 address",
						tslog.Addr("addr", addr),
						tslog.Uint("validLifetime", p.addr4ValidLifetime),
					)
				}
				p.addr4 = netip.Addr{}
				p.addr4ValidLifetime = 0
				return true

			case p.addr6:
				if p.logger.Enabled(slog.LevelDebug) {
					p.logger.Debug("Removing cached IPv6 address",
						tslog.Addr("addr", addr),
						tslog.Uint("validLifetime", p.addr6ValidLifetime),
					)
				}
				p.addr6 = netip.Addr{}
				p.addr6ValidLifetime = 0
				return true

			default:
				return false
			}
		}

		// Retrieve full address information.
		row := iphlpapi.MibUnicastIpAddressRow{
			Address:        nmsg.address,
			InterfaceLuid:  nmsg.interfaceLuid,
			InterfaceIndex: nmsg.interfaceIndex,
		}

		if err := iphlpapi.GetUnicastIpAddressEntry(&row); err != nil {
			p.logger.Error("Failed to get IP address information for IP address change notification",
				tslog.Addr("addr", addr),
				tslog.Uint("luid", nmsg.interfaceLuid),
				tslog.Uint("index", nmsg.interfaceIndex),
				tslog.Uint("type", nmsg.notificationType),
				tslog.Err(os.NewSyscallError("GetUnicastIpAddressEntry", err)),
			)
			return false
		}

		if p.logger.Enabled(slog.LevelDebug) {
			p.logger.Debug("Processing IP address change notification",
				tslog.Addr("addr", addr),
				tslog.Uint("luid", nmsg.interfaceLuid),
				tslog.Uint("index", nmsg.interfaceIndex),
				tslog.Uint("type", nmsg.notificationType),
				tslog.Uint("prefixOrigin", row.PrefixOrigin),
				tslog.Uint("suffixOrigin", row.SuffixOrigin),
				tslog.Uint("validLifetime", row.ValidLifetime),
				tslog.Uint("preferredLifetime", row.PreferredLifetime),
				tslog.Uint("onLinkPrefixLength", row.OnLinkPrefixLength),
				tslog.Uint("skipAsSource", row.SkipAsSource),
				tslog.Uint("dadState", row.DadState),
				tslog.Uint("scopeId", row.ScopeId),
			)
		}

		switch addr {
		case p.addr4:
			if row.DadState == iphlpapi.IpDadStateDeprecated {
				if p.logger.Enabled(slog.LevelDebug) {
					p.logger.Debug("Deprioritizing deprecated cached IPv4 address",
						tslog.Addr("addr", addr),
						tslog.Uint("validLifetime", row.ValidLifetime),
					)
				}
				p.addr4ValidLifetime = 0
				return false
			}

			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Updating cached IPv4 address valid lifetime",
					tslog.Addr("addr", addr),
					tslog.Uint("oldValidLifetime", p.addr4ValidLifetime),
					tslog.Uint("newValidLifetime", row.ValidLifetime),
				)
			}
			p.addr4ValidLifetime = row.ValidLifetime
			return false

		case p.addr6:
			if row.DadState == iphlpapi.IpDadStateDeprecated {
				if p.logger.Enabled(slog.LevelDebug) {
					p.logger.Debug("Deprioritizing deprecated cached IPv6 address",
						tslog.Addr("addr", addr),
						tslog.Uint("validLifetime", row.ValidLifetime),
					)
				}
				p.addr6ValidLifetime = 0
				return false
			}

			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Updating cached IPv6 address valid lifetime",
					tslog.Addr("addr", addr),
					tslog.Uint("oldValidLifetime", p.addr6ValidLifetime),
					tslog.Uint("newValidLifetime", row.ValidLifetime),
				)
			}
			p.addr6ValidLifetime = row.ValidLifetime
			return false

		default: // only on MibAddInstance
			// Skip temporary and deprecated addresses.
			if row.SuffixOrigin == iphlpapi.IpSuffixOriginRandom ||
				row.DadState == iphlpapi.IpDadStateDeprecated {
				return false
			}

			if addr.Is4() {
				if row.ValidLifetime < p.addr4ValidLifetime {
					return false
				}
				if p.logger.Enabled(slog.LevelDebug) {
					p.logger.Debug("Updating cached IPv4 address",
						tslog.Addr("oldAddr", p.addr4),
						tslog.Uint("oldValidLifetime", p.addr4ValidLifetime),
						tslog.Addr("newAddr", addr),
						tslog.Uint("newValidLifetime", row.ValidLifetime),
					)
				}
				p.addr4 = addr
				p.addr4ValidLifetime = row.ValidLifetime
			} else {
				if row.ValidLifetime < p.addr6ValidLifetime {
					return false
				}
				if p.logger.Enabled(slog.LevelDebug) {
					p.logger.Debug("Updating cached IPv6 address",
						tslog.Addr("oldAddr", p.addr6),
						tslog.Uint("oldValidLifetime", p.addr6ValidLifetime),
						tslog.Addr("newAddr", addr),
						tslog.Uint("newValidLifetime", row.ValidLifetime),
					)
				}
				p.addr6 = addr
				p.addr6ValidLifetime = row.ValidLifetime
			}

			return true
		}

	case iphlpapi.MibInitialNotification:
		// Drop the 2nd initial notification.
		select {
		case nmsg := <-p.notifyCh:
			if p.logger.Enabled(slog.LevelDebug) {
				p.logger.Debug("Dropped 2nd initial IP address change notification",
					tslog.Uint("luid", nmsg.interfaceLuid),
					tslog.Uint("index", nmsg.interfaceIndex),
					tslog.Uint("type", nmsg.notificationType),
				)
			}
		default:
		}

		var err error
		p.addr4, p.addr6, p.addr4ValidLifetime, p.addr6ValidLifetime, err = p.source.getAdaptersAddresses()
		if err != nil {
			p.logger.Error("Failed to get interface IP addresses", tslog.Err(err))
			return false
		}
		return true

	default:
		p.logger.Warn("Unknown IP address change notification type",
			tslog.Uint("luid", nmsg.interfaceLuid),
			tslog.Uint("index", nmsg.interfaceIndex),
			tslog.Uint("type", nmsg.notificationType),
		)
		return false
	}
}
