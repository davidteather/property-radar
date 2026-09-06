package domain

// Currency exists as a seam for non-USD expansion; v0 is USD-only.
type Currency string

const CurrencyUSD Currency = "USD"
