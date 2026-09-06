// Package ptr provides pointer helpers for the nil-as-unknown convention used
// throughout the domain.
package ptr

func To[T any](v T) *T { return &v }

func Deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func Clone[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func First[T any](vals ...*T) *T {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}
