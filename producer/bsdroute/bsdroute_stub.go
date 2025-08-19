//go:build !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package bsdroute

import (
	"context"

	producerpkg "github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/tslog"
)

type source struct{}

func newSource(_ string) (*Source, error) {
	return nil, ErrPlatformUnsupported
}

func (source) snapshot() (producerpkg.Message, error) {
	return producerpkg.Message{}, ErrPlatformUnsupported
}

func (*ProducerConfig) newProducer(_ *tslog.Logger) (*Producer, error) {
	return nil, ErrPlatformUnsupported
}

type producer struct{}

func (producer) subscribe() <-chan producerpkg.Message {
	panic(ErrPlatformUnsupported)
}

func (producer) run(_ context.Context) {
	panic(ErrPlatformUnsupported)
}
