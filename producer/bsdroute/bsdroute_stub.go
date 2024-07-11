//go:build !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package bsdroute

import "github.com/database64128/ddns-go/producer"

func newSource(_ string) (*Source, error) {
	return nil, ErrPlatformUnsupported
}

func (s *Source) snapshot() (producer.Message, error) {
	return producer.Message{}, ErrPlatformUnsupported
}
