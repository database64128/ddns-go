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
	"time"
	"unsafe"

	producerpkg "github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/producer/internal/broadcaster"
	"github.com/database64128/ddns-go/producer/win32iphlp/internal/iphlpapi"
	"golang.org/x/sys/windows"
)

type source struct {
	name string
	buf  []byte
	luid uint64
}

func newSource(name string) (*Source, error) {
	return &Source{source: source{name: name}}, nil
}

func (s *source) snapshot() (producerpkg.Message, error) {
	const (
		GAA_FLAG_SKIP_ANYCAST    = 0x2
		GAA_FLAG_SKIP_MULTICAST  = 0x4
		GAA_FLAG_SKIP_DNS_SERVER = 0x8
	)

	const maxTries = 3
	size := uint32(max(15000, cap(s.buf))) // recommended initial size 15 KB
	for range maxTries {
		s.buf = slices.Grow(s.buf[:0], int(size))
		s.buf = s.buf[:size]
		p := (*windows.IpAdapterAddresses)(unsafe.Pointer(unsafe.SliceData(s.buf)))
		if err := windows.GetAdaptersAddresses(
			windows.AF_UNSPEC,
			GAA_FLAG_SKIP_ANYCAST|GAA_FLAG_SKIP_MULTICAST|GAA_FLAG_SKIP_DNS_SERVER,
			0,
			p,
			&size,
		); err != nil {
			if err != windows.ERROR_BUFFER_OVERFLOW || size <= uint32(len(s.buf)) {
				return producerpkg.Message{}, os.NewSyscallError("GetAdaptersAddresses", err)
			}
			continue
		}
		if size == 0 {
			p = nil
		}
		return s.parseAdapterAddresses(p)
	}
	return producerpkg.Message{}, errors.New("ran out of tries for GetAdaptersAddresses")
}

func (s *source) parseAdapterAddresses(aa *windows.IpAdapterAddresses) (msg producerpkg.Message, err error) {
	for ; aa != nil; aa = aa.Next {
		if s.luid != 0 {
			// We have the interface LUID, no need to compare the name.
			if s.luid != aa.Luid {
				continue
			}
		} else {
			// Compare the name and store the LUID.
			if s.name != windows.UTF16PtrToString(aa.FriendlyName) {
				continue
			}
			s.luid = aa.Luid
		}

		var (
			v4ValidLifetime uint32
			v6ValidLifetime uint32
		)

		for ua := aa.FirstUnicastAddress; ua != nil; ua = ua.Next {
			// Skip temporary and deprecated addresses.
			if ua.SuffixOrigin == iphlpapi.IpSuffixOriginRandom ||
				ua.DadState == iphlpapi.IpDadStateDeprecated {
				continue
			}

			switch ua.Address.Sockaddr.Addr.Family {
			case windows.AF_INET:
				if ua.ValidLifetime <= v4ValidLifetime {
					continue
				}
				v4ValidLifetime = ua.ValidLifetime
				rsa := (*windows.RawSockaddrInet4)(unsafe.Pointer(ua.Address.Sockaddr))
				ip := netip.AddrFrom4(rsa.Addr)
				if ip.IsLinkLocalUnicast() {
					continue
				}
				msg.IPv4 = ip

			case windows.AF_INET6:
				if ua.ValidLifetime <= v6ValidLifetime {
					continue
				}
				v6ValidLifetime = ua.ValidLifetime
				rsa := (*windows.RawSockaddrInet6)(unsafe.Pointer(ua.Address.Sockaddr))
				ip := netip.AddrFrom16(rsa.Addr)
				if ip.IsLinkLocalUnicast() {
					continue
				}
				msg.IPv6 = ip
			}
		}

		return msg, nil
	}

	return producerpkg.Message{}, fmt.Errorf("no such network interface: %q", s.name)
}

func (cfg *ProducerConfig) newProducer() (*Producer, error) {
	if cfg.Interface == "" {
		return nil, errors.New("interface name is required")
	}
	return &Producer{
		producer: producer{
			// It's been observed that NotifyIpInterfaceChange sends 2 initial notifications and
			// blocks until the callback calls return. Be safe here and give it 2 extra slots.
			notifyCh: make(chan mibNotification, 4),
			source: source{
				name: cfg.Interface,
			},
			broadcaster: broadcaster.New(),
		},
	}, nil
}

type mibNotification struct {
	notificationType uint32
	luid             uint64
}

type producer struct {
	notifyCh    chan mibNotification
	source      source
	idleTimer   *time.Timer
	broadcaster *broadcaster.Broadcaster
}

func (p *Producer) subscribe() <-chan producerpkg.Message {
	return p.broadcaster.Subscribe()
}

var notifyIpInterfaceChangeCallback = sync.OnceValue(func() uintptr {
	return syscall.NewCallback(func(callerContext *chan<- mibNotification, row *iphlpapi.MibIpInterfaceRowLite, notificationType uint32) uintptr {
		notifyCh := *callerContext
		nmsg := mibNotification{notificationType: notificationType}
		if row != nil {
			nmsg.luid = row.InterfaceLuid
		}
		notifyCh <- nmsg
		return 0
	})
})

func (p *Producer) run(ctx context.Context, logger *slog.Logger) error {
	var notificationHandle windows.Handle

	if err := iphlpapi.NotifyIpInterfaceChange(
		windows.AF_UNSPEC,
		notifyIpInterfaceChangeCallback(),
		unsafe.Pointer(&p.notifyCh),
		true,
		&notificationHandle,
	); err != nil {
		return os.NewSyscallError("NotifyIpInterfaceChange", err)
	}

	logger.LogAttrs(ctx, slog.LevelInfo, "Registered for IP interface change notifications",
		slog.Uint64("notificationHandle", uint64(notificationHandle)),
	)

	defer func() {
		// Apparently, even on success, the notification handle can be NULL!
		// I mean, WTF, Microsoft?!
		if notificationHandle == 0 {
			logger.LogAttrs(ctx, slog.LevelDebug, "Skipping CancelMibChangeNotify2 because notification handle is NULL")
			return
		}
		if err := iphlpapi.CancelMibChangeNotify2(notificationHandle); err != nil {
			logger.LogAttrs(ctx, slog.LevelError, "Failed to cancel IP interface change notification",
				slog.Uint64("notificationHandle", uint64(notificationHandle)),
				slog.Any("error", err),
			)
		}
	}()

	done := ctx.Done()
	for {
		select {
		case <-done:
			return nil
		case nmsg := <-p.notifyCh:
			logger.LogAttrs(ctx, slog.LevelDebug, "Received IP interface change notification",
				slog.Uint64("type", uint64(nmsg.notificationType)),
				slog.Uint64("luid", nmsg.luid),
			)

			switch nmsg.notificationType {
			case iphlpapi.MibParameterNotification, iphlpapi.MibAddInstance:
				// Skip notifications for irrelevant interfaces.
				if p.source.luid != 0 && p.source.luid != nmsg.luid {
					continue
				}

				// Absorb rapid succession of notifications.
				p.waitUntilNotifyChIdle(ctx, logger)

			case iphlpapi.MibDeleteInstance:
				continue

			case iphlpapi.MibInitialNotification:
				// Drop the 2nd initial notification.
				select {
				case nmsg := <-p.notifyCh:
					logger.LogAttrs(ctx, slog.LevelDebug, "Dropped 2nd initial IP interface change notification",
						slog.Uint64("type", uint64(nmsg.notificationType)),
						slog.Uint64("luid", nmsg.luid),
					)
				default:
				}

			default:
				logger.LogAttrs(ctx, slog.LevelWarn, "Unknown IP interface change notification type",
					slog.Uint64("type", uint64(nmsg.notificationType)),
					slog.Uint64("luid", nmsg.luid),
				)
			}

			msg, err := p.source.snapshot()
			if err != nil {
				logger.LogAttrs(ctx, slog.LevelError, "Failed to get interface IP addresses", slog.Any("error", err))
				continue
			}
			logger.LogAttrs(ctx, slog.LevelInfo, "Broadcasting interface IP addresses",
				slog.Uint64("luid", p.source.luid),
				slog.Any("v4", msg.IPv4),
				slog.Any("v6", msg.IPv6),
			)
			p.broadcaster.Broadcast(msg)
		}
	}
}

// Wait until there's no activity on watched interface for 5 seconds.
const notifyChIdleWait = 5 * time.Second

func (p *producer) waitUntilNotifyChIdle(ctx context.Context, logger *slog.Logger) {
	p.initOrResetIdleTimer()
	done := ctx.Done()
	for {
		select {
		case <-done:
			if !p.idleTimer.Stop() {
				<-p.idleTimer.C
			}
			return
		case nmsg := <-p.notifyCh:
			logger.LogAttrs(ctx, slog.LevelDebug, "Received IP interface change notification",
				slog.Uint64("type", uint64(nmsg.notificationType)),
				slog.Uint64("luid", nmsg.luid),
			)
			if p.source.luid != 0 && p.source.luid != nmsg.luid {
				continue
			}
			if !p.idleTimer.Stop() {
				<-p.idleTimer.C
			}
			_ = p.idleTimer.Reset(notifyChIdleWait)
		case <-p.idleTimer.C:
			return
		}
	}
}

func (p *producer) initOrResetIdleTimer() {
	if p.idleTimer == nil {
		p.idleTimer = time.NewTimer(notifyChIdleWait)
	} else {
		_ = p.idleTimer.Reset(notifyChIdleWait)
	}
}
