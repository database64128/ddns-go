package rtnetlink

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

/*
#define NLMSG_ALIGN(len) ( ((len)+NLMSG_ALIGNTO-1) & ~(NLMSG_ALIGNTO-1) )
#define NLMSG_HDRLEN	 ((int) NLMSG_ALIGN(sizeof(struct nlmsghdr)))
#define NLMSG_LENGTH(len) ((len) + NLMSG_HDRLEN)
#define NLMSG_SPACE(len) NLMSG_ALIGN(NLMSG_LENGTH(len))
#define NLMSG_DATA(nlh)  ((void *)(((char *)nlh) + NLMSG_HDRLEN))
#define NLMSG_NEXT(nlh,len)	 ((len) -= NLMSG_ALIGN((nlh)->nlmsg_len), \
				  (struct nlmsghdr *)(((char *)(nlh)) + \
				  NLMSG_ALIGN((nlh)->nlmsg_len)))
#define NLMSG_OK(nlh,len) ((len) >= (int)sizeof(struct nlmsghdr) && \
			   (nlh)->nlmsg_len >= sizeof(struct nlmsghdr) && \
			   (nlh)->nlmsg_len <= (len))
#define NLMSG_PAYLOAD(nlh,len) ((nlh)->nlmsg_len - NLMSG_SPACE((len)))
*/

func NlMsgAlign(length uint32) uint32 {
	return (length + unix.NLMSG_ALIGNTO - 1) & ^uint32(unix.NLMSG_ALIGNTO-1)
}

func NlMsgLength(length uint32) uint32 {
	return length + unix.NLMSG_HDRLEN
}

func NlMsgSpace(length uint32) uint32 {
	return NlMsgAlign(NlMsgLength(length))
}

func NlMsgData(nlh *unix.NlMsghdr) unsafe.Pointer {
	return unsafe.Add(unsafe.Pointer(nlh), unix.NLMSG_HDRLEN)
}

func NlMsgNext(nlh *unix.NlMsghdr, length *uint32) *unix.NlMsghdr {
	*length -= NlMsgAlign(nlh.Len)
	return (*unix.NlMsghdr)(unsafe.Add(unsafe.Pointer(nlh), NlMsgAlign(nlh.Len)))
}

func NlMsgOK(nlh *unix.NlMsghdr, length uint32) bool {
	return length >= unix.SizeofNlMsghdr &&
		nlh.Len >= unix.SizeofNlMsghdr &&
		nlh.Len <= length
}

func NlMsgPayload(nlh *unix.NlMsghdr, length uint32) int {
	return int(nlh.Len) - int(NlMsgSpace(length))
}

/*
#define NLA_ALIGN(len)		(((len) + NLA_ALIGNTO - 1) & ~(NLA_ALIGNTO - 1))
*/

func NlaAlign(length uint16) uint16 {
	return (length + unix.NLA_ALIGNTO - 1) & ^uint16(unix.NLA_ALIGNTO-1)
}

/*
#define RTA_ALIGN(len) ( ((len)+RTA_ALIGNTO-1) & ~(RTA_ALIGNTO-1) )
#define RTA_OK(rta,len) ((len) >= (int)sizeof(struct rtattr) && \
			 (rta)->rta_len >= sizeof(struct rtattr) && \
			 (rta)->rta_len <= (len))
#define RTA_NEXT(rta,attrlen)	((attrlen) -= RTA_ALIGN((rta)->rta_len), \
				 (struct rtattr*)(((char*)(rta)) + RTA_ALIGN((rta)->rta_len)))
#define RTA_LENGTH(len)	(RTA_ALIGN(sizeof(struct rtattr)) + (len))
#define RTA_SPACE(len)	RTA_ALIGN(RTA_LENGTH(len))
#define RTA_DATA(rta)   ((void*)(((char*)(rta)) + RTA_LENGTH(0)))
#define RTA_PAYLOAD(rta) ((int)((rta)->rta_len) - RTA_LENGTH(0))
*/

func RtaAlign(length uint16) uint16 {
	return (length + unix.RTA_ALIGNTO - 1) & ^uint16(unix.RTA_ALIGNTO-1)
}

func RtaOK(rta *unix.RtAttr, length uint16) bool {
	return length >= unix.SizeofRtAttr &&
		rta.Len >= unix.SizeofRtAttr &&
		rta.Len <= length
}

func RtaNext(rta *unix.RtAttr, length *uint16) *unix.RtAttr {
	*length -= RtaAlign(rta.Len)
	return (*unix.RtAttr)(unsafe.Add(unsafe.Pointer(rta), RtaAlign(rta.Len)))
}

func RtaLength(length uint16) uint16 {
	return RtaAlign(unix.SizeofRtAttr) + length
}

func RtaSpace(length uint16) uint16 {
	return RtaAlign(RtaLength(length))
}

func RtaData(rta *unix.RtAttr) unsafe.Pointer {
	return unsafe.Add(unsafe.Pointer(rta), RtaLength(0))
}

func RtaPayload(rta *unix.RtAttr) int {
	return int(rta.Len) - int(RtaLength(0))
}
