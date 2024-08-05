package iphlpapi

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	IF_MAX_PHYS_ADDRESS_LENGTH = 32
	IF_MAX_STRING_SIZE         = 256
)

/*
MIB_IF_ENTRY_LEVEL enumeration from netioapi.h or
https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-getifentry2ex.

	typedef enum _MIB_IF_ENTRY_LEVEL {
		MibIfEntryNormal = 0,
		MibIfEntryNormalWithoutStatistics = 2
	} MIB_IF_ENTRY_LEVEL, *PMIB_IF_ENTRY_LEVEL;
*/
const (
	MibIfEntryNormal                  = 0
	MibIfEntryNormalWithoutStatistics = 2
)

/*
NL_PREFIX_ORIGIN enumeration from nldef.h or
https://learn.microsoft.com/en-us/windows/win32/api/nldef/ne-nldef-nl_prefix_origin.

	typedef enum {
		//
		// These values are from iptypes.h.
		// They need to fit in a 4 bit field.
		//
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
NL_SUFFIX_ORIGIN enumeration from nldef.h or
https://learn.microsoft.com/en-us/windows/win32/api/nldef/ne-nldef-nl_suffix_origin.

	typedef enum {
		//
		// These values are from in iptypes.h.
		// They need to fit in a 4 bit field.
		//
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
	IpSuffixOriginOther = iota
	IpSuffixOriginManual
	IpSuffixOriginWellKnown
	IpSuffixOriginDhcp
	IpSuffixOriginLinkLayerAddress
	IpSuffixOriginRandom
	IpSuffixOriginUnchanged = 1 << 4
)

/*
NL_DAD_STATE enumeration from nldef.h or
https://learn.microsoft.com/en-us/windows/win32/api/nldef/ne-nldef-nl_dad_state.

	typedef enum {
		//
		// These values are from in iptypes.h.
		//
		IpDadStateInvalid    = 0,
		IpDadStateTentative,
		IpDadStateDuplicate,
		IpDadStateDeprecated,
		IpDadStatePreferred,
	} NL_DAD_STATE;
*/
const (
	IpDadStateInvalid = iota
	IpDadStateTentative
	IpDadStateDuplicate
	IpDadStateDeprecated
	IpDadStatePreferred
)

/*
MIB_NOTIFICATION_TYPE enumeration from netioapi.h or
https://learn.microsoft.com/en-us/windows/win32/api/netioapi/ne-netioapi-mib_notification_type.

	typedef enum _MIB_NOTIFICATION_TYPE {
		//
		// ParameterChange.
		//
		MibParameterNotification,
		//
		// Addition.
		//
		MibAddInstance,
		//
		// Deletion.
		//
		MibDeleteInstance,
		//
		// Initial notification.
		//
		MibInitialNotification,
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

	procGetIfEntry2Ex                = modiphlpapi.NewProc("GetIfEntry2Ex")
	procGetUnicastIpAddressEntry     = modiphlpapi.NewProc("GetUnicastIpAddressEntry")
	procNotifyIpInterfaceChange      = modiphlpapi.NewProc("NotifyIpInterfaceChange")
	procNotifyUnicastIpAddressChange = modiphlpapi.NewProc("NotifyUnicastIpAddressChange")
	procCancelMibChangeNotify2       = modiphlpapi.NewProc("CancelMibChangeNotify2")
)

/*
MibIfRow2 stores information about a particular interface.

From https://learn.microsoft.com/en-us/windows/win32/api/netioapi/ns-netioapi-mib_if_row2:

	typedef struct _MIB_IF_ROW2 {
		NET_LUID                   InterfaceLuid;
		NET_IFINDEX                InterfaceIndex;
		GUID                       InterfaceGuid;
		WCHAR                      Alias[IF_MAX_STRING_SIZE + 1];
		WCHAR                      Description[IF_MAX_STRING_SIZE + 1];
		ULONG                      PhysicalAddressLength;
		UCHAR                      PhysicalAddress[IF_MAX_PHYS_ADDRESS_LENGTH];
		UCHAR                      PermanentPhysicalAddress[IF_MAX_PHYS_ADDRESS_LENGTH];
		ULONG                      Mtu;
		IFTYPE                     Type;
		TUNNEL_TYPE                TunnelType;
		NDIS_MEDIUM                MediaType;
		NDIS_PHYSICAL_MEDIUM       PhysicalMediumType;
		NET_IF_ACCESS_TYPE         AccessType;
		NET_IF_DIRECTION_TYPE      DirectionType;
		struct {
			BOOLEAN HardwareInterface : 1;
			BOOLEAN FilterInterface : 1;
			BOOLEAN ConnectorPresent : 1;
			BOOLEAN NotAuthenticated : 1;
			BOOLEAN NotMediaConnected : 1;
			BOOLEAN Paused : 1;
			BOOLEAN LowPower : 1;
			BOOLEAN EndPointInterface : 1;
		} InterfaceAndOperStatusFlags;
		IF_OPER_STATUS             OperStatus;
		NET_IF_ADMIN_STATUS        AdminStatus;
		NET_IF_MEDIA_CONNECT_STATE MediaConnectState;
		NET_IF_NETWORK_GUID        NetworkGuid;
		NET_IF_CONNECTION_TYPE     ConnectionType;
		ULONG64                    TransmitLinkSpeed;
		ULONG64                    ReceiveLinkSpeed;
		ULONG64                    InOctets;
		ULONG64                    InUcastPkts;
		ULONG64                    InNUcastPkts;
		ULONG64                    InDiscards;
		ULONG64                    InErrors;
		ULONG64                    InUnknownProtos;
		ULONG64                    InUcastOctets;
		ULONG64                    InMulticastOctets;
		ULONG64                    InBroadcastOctets;
		ULONG64                    OutOctets;
		ULONG64                    OutUcastPkts;
		ULONG64                    OutNUcastPkts;
		ULONG64                    OutDiscards;
		ULONG64                    OutErrors;
		ULONG64                    OutUcastOctets;
		ULONG64                    OutMulticastOctets;
		ULONG64                    OutBroadcastOctets;
		ULONG64                    OutQLen;
	} MIB_IF_ROW2, *PMIB_IF_ROW2;
*/
type MibIfRow2 struct {
	InterfaceLuid               uint64
	InterfaceIndex              uint32
	InterfaceGuid               windows.GUID
	Alias                       [IF_MAX_STRING_SIZE + 1]uint16
	Description                 [IF_MAX_STRING_SIZE + 1]uint16
	PhysicalAddressLength       uint32
	PhysicalAddress             [IF_MAX_PHYS_ADDRESS_LENGTH]uint8
	PermanentPhysicalAddress    [IF_MAX_PHYS_ADDRESS_LENGTH]uint8
	Mtu                         uint32
	Type                        uint32
	TunnelType                  uint32
	MediaType                   uint32
	PhysicalMediumType          uint32
	AccessType                  uint32
	DirectionType               uint32
	InterfaceAndOperStatusFlags uint8
	OperStatus                  uint32
	AdminStatus                 uint32
	MediaConnectState           uint32
	NetworkGuid                 windows.GUID
	ConnectionType              uint32
	TransmitLinkSpeed           uint64
	ReceiveLinkSpeed            uint64
	InOctets                    uint64
	InUcastPkts                 uint64
	InNUcastPkts                uint64
	InDiscards                  uint64
	InErrors                    uint64
	InUnknownProtos             uint64
	InUcastOctets               uint64
	InMulticastOctets           uint64
	InBroadcastOctets           uint64
	OutOctets                   uint64
	OutUcastPkts                uint64
	OutNUcastPkts               uint64
	OutDiscards                 uint64
	OutErrors                   uint64
	OutUcastOctets              uint64
	OutMulticastOctets          uint64
	OutBroadcastOctets          uint64
	OutQLen                     uint64
}

/*
MIB_UNICASTIPADDRESS_ROW stores information about a unicast IP address.

From https://learn.microsoft.com/en-us/windows/win32/api/netioapi/ns-netioapi-mib_unicastipaddress_row:

	typedef struct _MIB_UNICASTIPADDRESS_ROW {
		SOCKADDR_INET    Address;
		NET_LUID         InterfaceLuid;
		NET_IFINDEX      InterfaceIndex;
		NL_PREFIX_ORIGIN PrefixOrigin;
		NL_SUFFIX_ORIGIN SuffixOrigin;
		ULONG            ValidLifetime;
		ULONG            PreferredLifetime;
		UINT8            OnLinkPrefixLength;
		BOOLEAN          SkipAsSource;
		NL_DAD_STATE     DadState;
		SCOPE_ID         ScopeId;
		LARGE_INTEGER    CreationTimeStamp;
	} MIB_UNICASTIPADDRESS_ROW, *PMIB_UNICASTIPADDRESS_ROW;
*/
type MibUnicastIpAddressRow struct {
	Address            windows.RawSockaddrInet6 // SOCKADDR_INET union
	InterfaceLuid      uint64
	InterfaceIndex     uint32
	PrefixOrigin       uint32
	SuffixOrigin       uint32
	ValidLifetime      uint32
	PreferredLifetime  uint32
	OnLinkPrefixLength uint8
	SkipAsSource       uint8
	DadState           uint32
	ScopeId            uint32
	CreationTimeStamp  windows.Filetime
}

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
GetIfEntry2Ex retrieves the specified level of information for the specified interface on the local computer.

From https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-getifentry2ex:

	IPHLPAPI_DLL_LINKAGE _NETIOAPI_SUCCESS_ NETIOAPI_API GetIfEntry2Ex(
		[in]      MIB_IF_ENTRY_LEVEL Level,
		[in, out] PMIB_IF_ROW2       Row
	);
*/
func GetIfEntry2Ex(level uint32, row *MibIfRow2) error {
	ret, _, _ := syscall.SyscallN(
		procGetIfEntry2Ex.Addr(),
		uintptr(level),
		uintptr(unsafe.Pointer(row)),
	)
	if ret != windows.NO_ERROR {
		return syscall.Errno(ret)
	}
	return nil
}

/*
GetUnicastIpAddressEntry retrieves information for an existing unicast IP address entry on the local computer.

From https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-getunicastipaddressentry:

	IPHLPAPI_DLL_LINKAGE _NETIOAPI_SUCCESS_ NETIOAPI_API GetUnicastIpAddressEntry(
		[in, out] PMIB_UNICASTIPADDRESS_ROW Row
	);
*/
func GetUnicastIpAddressEntry(row *MibUnicastIpAddressRow) error {
	ret, _, _ := syscall.SyscallN(
		procGetUnicastIpAddressEntry.Addr(),
		uintptr(unsafe.Pointer(row)),
	)
	if ret != windows.NO_ERROR {
		return syscall.Errno(ret)
	}
	return nil
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
		uintptr(callerContext),
		uintptr(boolToUint8(initialNotification)),
		uintptr(unsafe.Pointer(notificationHandle)),
	)
	if ret != windows.NO_ERROR {
		return syscall.Errno(ret)
	}
	return nil
}

/*
NotifyUnicastIpAddressChange registers to be notified for changes to all unicast IP interfaces, unicast IPv4 addresses, or unicast IPv6 addresses on a local computer.

From https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-notifyunicastipaddresschange:

	IPHLPAPI_DLL_LINKAGE _NETIOAPI_SUCCESS_ NETIOAPI_API NotifyUnicastIpAddressChange(
		[in]      ADDRESS_FAMILY                     Family,
		[in]      PUNICAST_IPADDRESS_CHANGE_CALLBACK Callback,
		[in]      PVOID                              CallerContext,
		[in]      BOOLEAN                            InitialNotification,
		[in, out] HANDLE                             *NotificationHandle
	);

The Family parameter must be set to either AF_INET, AF_INET6, or AF_UNSPEC.

The invocation of the callback function specified in the Callback parameter is serialized. The callback function should be defined as a function of type VOID. The parameters passed to the callback function include the following:

  - IN PVOID CallerContext:                    The CallerContext parameter passed to the NotifyUnicastIpAddressChange function when registering for notifications.
  - IN PMIB_UNICASTIPADDRESS_ROW Row OPTIONAL: A pointer to the MIB_UNICASTIPADDRESS_ROW entry for the unicast IP address that was changed. This parameter is a NULL pointer when the MIB_NOTIFICATION_TYPE value passed in the NotificationType parameter to the callback function is set to MibInitialNotification. This can only occur if the InitialNotification parameter passed to NotifyUnicastIpAddressChange was set to TRUE when registering for notifications.
  - IN MIB_NOTIFICATION_TYPE NotificationType: The notification type. This member can be one of the values from the MIB_NOTIFICATION_TYPE enumeration type defined in the Netioapi.h header file.
*/
func NotifyUnicastIpAddressChange(
	family uint16,
	callback uintptr,
	callerContext unsafe.Pointer,
	initialNotification bool,
	notificationHandle *windows.Handle,
) error {
	ret, _, _ := syscall.SyscallN(
		procNotifyUnicastIpAddressChange.Addr(),
		uintptr(family),
		callback,
		uintptr(callerContext),
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
