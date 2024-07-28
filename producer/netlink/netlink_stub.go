//go:build !linux

package netlink

import (
	"context"

	producerpkg "github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/tslog"
)

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
