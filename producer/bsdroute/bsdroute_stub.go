//go:build !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package bsdroute

import "github.com/database64128/ddns-go/producer"

type source struct{}

func newSource(_ string) (*Source, error) {
	return nil, ErrPlatformUnsupported
}

func (source) snapshot() (producer.Message, error) {
	return producer.Message{}, ErrPlatformUnsupported
}

func (*ProducerConfig) newProducer() (producer.Producer, error) {
	return nil, ErrPlatformUnsupported
}
