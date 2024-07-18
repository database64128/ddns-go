package iphlpapi

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

/*
From https://learn.microsoft.com/en-us/windows/win32/api/nldef/ne-nldef-nl_prefix_origin:

	typedef enum {
		IpPrefixOriginOther = 0,
		IpPrefixOriginManual,
		IpPrefixOriginWellKnown,
		IpPrefixOriginDhcp,
		IpPrefixOriginRouterAdvertisement,
		IpPrefixOriginUnchanged = 1 << 4
	} NL_PREFIX_ORIGIN;
*/
const (
	IpPrefixOriginOther = iota
	IpPrefixOriginManual
	IpPrefixOriginWellKnown
	IpPrefixOriginDhcp
	IpPrefixOriginRouterAdvertisement
	IpPrefixOriginUnchanged = 1 << 4
)

/*
From https://learn.microsoft.com/en-us/windows/win32/api/nldef/ne-nldef-nl_suffix_origin:

	typedef enum {
		NlsoOther = 0,
		NlsoManual,
		NlsoWellKnown,
		NlsoDhcp,
		NlsoLinkLayerAddress,
		NlsoRandom,
		IpSuffixOriginOther = 0,
		IpSuffixOriginManual,
		IpSuffixOriginWellKnown,
		IpSuffixOriginDhcp,
		IpSuffixOriginLinkLayerAddress,
		IpSuffixOriginRandom,
		IpSuffixOriginUnchanged = 1 << 4
	} NL_SUFFIX_ORIGIN;
*/
const (
	NlsoOther = iota
	NlsoManual
	NlsoWellKnown
	NlsoDhcp
	NlsoLinkLayerAddress
	NlsoRandom
	IpSuffixOriginOther = iota
	IpSuffixOriginManual
	IpSuffixOriginWellKnown
	IpSuffixOriginDhcp
	IpSuffixOriginLinkLayerAddress
	IpSuffixOriginRandom
	IpSuffixOriginUnchanged = 1 << 4
)

/*
From https://learn.microsoft.com/en-us/windows/win32/api/nldef/ne-nldef-nl_dad_state:

	typedef enum {
		NldsInvalid,
		NldsTentative,
		NldsDuplicate,
		NldsDeprecated,
		NldsPreferred,
		IpDadStateInvalid = 0,
		IpDadStateTentative,
		IpDadStateDuplicate,
		IpDadStateDeprecated,
		IpDadStatePreferred
	} NL_DAD_STATE;
*/
const (
	NldsInvalid = iota
	NldsTentative
	NldsDuplicate
	NldsDeprecated
	NldsPreferred
	IpDadStateInvalid = iota
	IpDadStateTentative
	IpDadStateDuplicate
	IpDadStateDeprecated
	IpDadStatePreferred
)

/*
From https://learn.microsoft.com/en-us/windows/win32/api/netioapi/ne-netioapi-mib_notification_type:

	typedef enum _MIB_NOTIFICATION_TYPE {
		MibParameterNotification,
		MibAddInstance,
		MibDeleteInstance,
		MibInitialNotification
	} MIB_NOTIFICATION_TYPE, *PMIB_NOTIFICATION_TYPE;
*/
const (
	MibParameterNotification = iota
	MibAddInstance
	MibDeleteInstance
	MibInitialNotification
)

var (
	modiphlpapi = windows.NewLazySystemDLL("iphlpapi.dll")

	procNotifyIpInterfaceChange = modiphlpapi.NewProc("NotifyIpInterfaceChange")
	procCancelMibChangeNotify2  = modiphlpapi.NewProc("CancelMibChangeNotify2")
)

/*
From https://learn.microsoft.com/en-us/windows/win32/api/netioapi/ns-netioapi-mib_ipinterface_row:

	typedef struct _MIB_IPINTERFACE_ROW {
		ADDRESS_FAMILY                 Family;
		NET_LUID                       InterfaceLuid;
		NET_IFINDEX                    InterfaceIndex;
		ULONG                          MaxReassemblySize;
		ULONG64                        InterfaceIdentifier;
		ULONG                          MinRouterAdvertisementInterval;
		ULONG                          MaxRouterAdvertisementInterval;
		BOOLEAN                        AdvertisingEnabled;
		BOOLEAN                        ForwardingEnabled;
		BOOLEAN                        WeakHostSend;
		BOOLEAN                        WeakHostReceive;
		BOOLEAN                        UseAutomaticMetric;
		BOOLEAN                        UseNeighborUnreachabilityDetection;
		BOOLEAN                        ManagedAddressConfigurationSupported;
		BOOLEAN                        OtherStatefulConfigurationSupported;
		BOOLEAN                        AdvertiseDefaultRoute;
		NL_ROUTER_DISCOVERY_BEHAVIOR   RouterDiscoveryBehavior;
		ULONG                          DadTransmits;
		ULONG                          BaseReachableTime;
		ULONG                          RetransmitTime;
		ULONG                          PathMtuDiscoveryTimeout;
		NL_LINK_LOCAL_ADDRESS_BEHAVIOR LinkLocalAddressBehavior;
		ULONG                          LinkLocalAddressTimeout;
		ULONG                          ZoneIndices[ScopeLevelCount];
		ULONG                          SitePrefixLength;
		ULONG                          Metric;
		ULONG                          NlMtu;
		BOOLEAN                        Connected;
		BOOLEAN                        SupportsWakeUpPatterns;
		BOOLEAN                        SupportsNeighborDiscovery;
		BOOLEAN                        SupportsRouterDiscovery;
		ULONG                          ReachableTime;
		NL_INTERFACE_OFFLOAD_ROD       TransmitOffload;
		NL_INTERFACE_OFFLOAD_ROD       ReceiveOffload;
		BOOLEAN                        DisableDefaultRoutes;
	} MIB_IPINTERFACE_ROW, *PMIB_IPINTERFACE_ROW;
*/

// MibIpInterfaceRowLite is a lightweight version of MIB_IPINTERFACE_ROW,
// containing only the first 3 fields, which are the only fields populated
// by [NotifyIpInterfaceChange].
type MibIpInterfaceRowLite struct {
	Family         uint16
	InterfaceLuid  uint64
	InterfaceIndex uint32
}

/*
NotifyIpInterfaceChange registers to be notified for changes to all IP interfaces, IPv4 interfaces, or IPv6 interfaces on a local computer.

From https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-notifyipinterfacechange:

	IPHLPAPI_DLL_LINKAGE _NETIOAPI_SUCCESS_ NETIOAPI_API NotifyIpInterfaceChange(
		[in]      ADDRESS_FAMILY               Family,
		[in]      PIPINTERFACE_CHANGE_CALLBACK Callback,
		[in]      PVOID                        CallerContext,
		[in]      BOOLEAN                      InitialNotification,
		[in, out] HANDLE                       *NotificationHandle
	);

The Family parameter must be set to either AF_INET, AF_INET6, or AF_UNSPEC.

The invocation of the callback function specified in the Callback parameter is serialized. The callback function should be defined as a function of type VOID. The parameters passed to the callback function include the following:

  - IN PVOID CallerContext:                    The CallerContext parameter passed to the NotifyIpInterfaceChange function when registering for notifications.
  - IN PMIB_IPINTERFACE_ROW Row OPTIONAL:      A pointer to the MIB_IPINTERFACE_ROW entry for the interface that was changed. This parameter is a NULL pointer when the MIB_NOTIFICATION_TYPE value passed in the NotificationType parameter to the callback function is set to MibInitialNotification. This can only occur if the InitialNotification parameter passed to NotifyIpInterfaceChange was set to TRUE when registering for notifications.
  - IN MIB_NOTIFICATION_TYPE NotificationType: The notification type. This member can be one of the values from the MIB_NOTIFICATION_TYPE enumeration type defined in the Netioapi.h header file.
*/
func NotifyIpInterfaceChange(
	family uint16,
	callback uintptr,
	callerContext unsafe.Pointer,
	initialNotification bool,
	notificationHandle *windows.Handle,
) error {
	ret, _, _ := syscall.SyscallN(
		procNotifyIpInterfaceChange.Addr(),
		uintptr(family),
		callback,
		uintptr(unsafe.Pointer(callerContext)),
		uintptr(boolToUint8(initialNotification)),
		uintptr(unsafe.Pointer(notificationHandle)),
	)
	if ret != windows.NO_ERROR {
		return syscall.Errno(ret)
	}
	return nil
}

func boolToUint8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

/*
CancelMibChangeNotify2 deregisters for change notifications for IP interface changes, IP address changes, IP route changes, Teredo port changes, and when the unicast IP address table is stable and can be retrieved.

From https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-cancelmibchangenotify2:

	IPHLPAPI_DLL_LINKAGE NETIOAPI_API CancelMibChangeNotify2(
		[in] HANDLE NotificationHandle
	);
*/
func CancelMibChangeNotify2(notificationHandle windows.Handle) error {
	ret, _, _ := syscall.SyscallN(
		procCancelMibChangeNotify2.Addr(),
		uintptr(notificationHandle),
	)
	if ret != windows.NO_ERROR {
		return syscall.Errno(ret)
	}
	return nil
}
