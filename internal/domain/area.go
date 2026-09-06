package domain

// Area is a provider's geographic unit (neighborhood, region, or borough),
// provider-agnostic so a second provider supplies its own catalog. ID is the
// crawl identifier; Level/ParentID encode the taxonomy (0 = root, larger = finer).
type Area struct {
	Provider string
	ID       string
	Name     string
	Short    string
	Borough  string
	Level    int
	ParentID string
}
