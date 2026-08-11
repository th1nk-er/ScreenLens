//go:build !windows && !darwin && !linux

package instance

type Lock struct{}

func Acquire(string) (*Lock, error) {
	return &Lock{}, nil
}

func (l *Lock) Close() error {
	return nil
}
