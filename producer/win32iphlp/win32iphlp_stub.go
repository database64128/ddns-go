//go:build !windows

package win32iphlp

import (
	"context"
	"log/slog"

	producerpkg "github.com/database64128/ddns-go/producer"
)

type source struct{}

func newSource(_ string) (*Source, error) {
	return nil, ErrPlatformUnsupported
}

func (source) snapshot() (producerpkg.Message, error) {
	return producerpkg.Message{}, ErrPlatformUnsupported
}

func (*ProducerConfig) newProducer() (*Producer, error) {
	return nil, ErrPlatformUnsupported
}

type producer struct{}

func (producer) subscribe() <-chan producerpkg.Message {
	panic(ErrPlatformUnsupported)
}

func (producer) run(_ context.Context, _ *slog.Logger) error {
	return ErrPlatformUnsupported
}
