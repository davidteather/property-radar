package domain

import "fmt"

// Money is whole US dollars; listings data is dollar-granular.
type Money int64

func (m Money) Dollars() int64 { return int64(m) }

func (m Money) String() string { return fmt.Sprintf("$%d", int64(m)) }
