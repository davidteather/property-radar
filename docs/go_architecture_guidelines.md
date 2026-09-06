# Go Architecture & Coding Conventions

> **Status:** Normative project guidance for humans and coding agents.
>
> **Intent:** Keep Go code idiomatic, explicit, testable, and easy to change without introducing unnecessary “enterprise architecture.”
>
> Keywords **MUST**, **SHOULD**, **MAY**, and **MUST NOT** are used deliberately.

---

## 1. Core principles

1. **Dependencies point toward application-owned concepts.**
2. **Domain types are the canonical internal representation.**
3. **External formats are translated at the boundary that owns them.**
4. **Interfaces are owned by consumers, not implementers.**
5. **Do not create interfaces merely to enable mocking.**
6. **Accept interfaces where behavior substitution is useful; return concrete types by default.**
7. **Packages should represent cohesive concepts/capabilities, not generic architectural buckets.**
8. **`main` is a composition root: configuration, construction, wiring, startup, shutdown.**
9. **Persistence owns persistence details.**
10. **Prefer a little direct code and duplication over speculative abstraction.**

A useful mental model is:

```text
external systems
    |
    v
provider / transport adapters
    |
    v
application workflows
    |
    v
domain
    ^
    |
store / persistence adapters

outbound API / MCP / HTTP
    ^
    |
application workflows
```

The most important property is not the exact directory tree. It is that external-system concerns, persistence concerns, and transport concerns do not leak into domain/application code.

---

## 2. Recommended project layout

For an application like the property/StreetEasy project:

```text
.
├── cmd/
│   └── app/
│       └── main.go
│
├── api/                         # Schema/contract assets, not a generic Go package.
│   └── proto/
│       └── property.proto
│
├── internal/
│   ├── domain/
│   │   ├── property.go
│   │   ├── realtor.go
│   │   ├── address.go
│   │   └── money.go
│   │
│   ├── listing/                 # Application/use-case package.
│   │   ├── service.go
│   │   ├── source.go            # Consumer-owned interfaces.
│   │   ├── store.go             # Consumer-owned interfaces.
│   │   └── mocks/               # Generated when genuinely useful.
│   │
│   ├── streeteasy/              # StreetEasy adapter.
│   │   ├── client.go
│   │   ├── wire.go              # StreetEasy/API response structs.
│   │   ├── convert.go           # StreetEasy -> domain conversion.
│   │   └── errors.go
│   │
│   ├── store/                   # Concrete persistence implementation.
│   │   ├── store.go
│   │   ├── property.go
│   │   ├── rows.go              # DB-shaped structs with db tags.
│   │   └── convert.go           # domain <-> persistence representation.
│   │
│   ├── httpapi/                 # HTTP-facing transport.
│   │   ├── server.go
│   │   ├── handlers.go
│   │   ├── types.go             # JSON request/response DTOs.
│   │   └── convert.go           # domain/application -> HTTP response.
│   │
│   ├── mcp/
│   │   ├── server.go
│   │   ├── tools.go
│   │   └── types.go
│   │
│   └── gen/                     # Generated Go code if applicable.
│       └── propertypb/
│
├── .mockery.yml
├── Makefile
├── go.mod
└── go.sum
```

### Layout rules

- Application-only packages **SHOULD** live under `internal/`.
- A top-level `api/` directory **MAY** contain `.proto`, OpenAPI, or other schema/contract files.
- Avoid a generic Go package literally named `api`, `types`, `interfaces`, `helpers`, `common`, or `utils`.
- Prefer transport-specific names such as `httpapi`, `mcp`, `grpc`, or generated `propertypb`.
- Packages **SHOULD** be created because they represent a cohesive concept, not simply because a diagram contains a “layer.”

---

## 3. Domain layer

The domain package contains the application-owned concepts that the rest of the application agrees on.

Examples:

```go
package domain

type PropertyID string

type Property struct {
	ID       PropertyID
	Address  Address
	Price    Money
	Bedrooms int
	Bathrooms float64
}

type Realtor struct {
	Name string
}
```

### Domain rules

Domain types:

- **MUST NOT** contain StreetEasy-specific response fields.
- **MUST NOT** contain `json`, `db`, protobuf, GraphQL, or vendor-specific tags merely to satisfy an external boundary.
- **MUST NOT** import database drivers, HTTP clients, generated protobuf packages, or provider-specific packages.
- **SHOULD** model stable application concepts rather than every field returned by a provider.
- **MAY** enforce meaningful invariants with constructors or value types when invalid states are consequential.
- **SHOULD NOT** become over-engineered DDD objects when plain structs are sufficient.

The domain is the internal lingua franca.

```text
StreetEasy JSON
      |
      v
domain.Property
      |
      +----------> persistence
      |
      +----------> HTTP response
      |
      +----------> MCP output
```

---

## 4. External provider adapters: StreetEasy, Zillow, etc.

### Decision: provider packages own their wire types and conversions

The default pattern is:

```text
streeteasy/
    client.go
    wire.go
    convert.go
```

Not:

```text
fetchers/
getters/
converters/
api_types/
```

A generic `fetchers` or `converters` package usually groups code by implementation mechanic rather than by cohesive responsibility. It tends to accumulate unrelated providers and dependencies over time.

### Preferred flow

```text
StreetEasy HTTP/JSON
        |
        v
streeteasy wire types
        |
        v
streeteasy conversion
        |
        v
domain.Property
```

Example:

```go
package streeteasy

type propertyResponse struct {
	ID       string   `json:"id"`
	Price    int64    `json:"price"`
	Beds     *float64 `json:"bedrooms"`
	Address  string   `json:"address"`
}

func propertyToDomain(p propertyResponse) domain.Property {
	// Normalize provider-specific representation here.
	return domain.Property{
		ID:      domain.PropertyID(p.ID),
		Address: domain.ParseAddress(p.Address),
		Price:   domain.NewMoneyUSD(p.Price),
		// ...
	}
}
```

Wire types **SHOULD normally be unexported** if no other package needs them.

### Provider client API

A provider package **SHOULD return its concrete client from its constructor**:

```go
func NewClient(httpClient *http.Client, cfg Config) *Client
```

Do not write this merely for abstraction:

```go
func NewClient(...) StreetEasyClient
```

The consumer decides which behavior it needs.

---

## 5. The shared abstraction is a capability, not a “fetcher” package

If the listing application can consume multiple property sources, define that capability in the application package that consumes it:

```go
package listing

type PropertySource interface {
	Search(ctx context.Context, query SearchQuery) ([]domain.Property, error)
}
```

Then:

```go
var _ listing.PropertySource = (*streeteasy.Client)(nil)
```

may be used as an optional compile-time assertion.

The dependency relationship is:

```text
listing.PropertySource   <- owned by consumer
          ^
          |
streeteasy.Client        <- concrete implementation
```

### Why this is cleaner than `fetchers/`

The application cares about:

> “Give me properties matching this query.”

It does **not** care that the implementation happens to “fetch” JSON.

A future implementation might read from:

- StreetEasy,
- Zillow,
- an offline dataset,
- a cache,
- a replay fixture,
- another service.

`PropertySource` describes the application capability. `Fetcher` describes an implementation mechanism.

### Interface rules

Interfaces:

- **MUST** be defined in the package that consumes the behavior unless the interface itself is the shared protocol/product.
- **MUST** contain only the methods the consumer needs.
- **SHOULD** remain unexported when only used internally by one package.
- **MUST NOT** be created merely because a concrete dependency exists.
- **MUST NOT** be created merely so Mockery can generate a mock.
- **SHOULD** usually be small.
- Constructors **SHOULD** return concrete types.

A good interface often appears only after application code demonstrates a genuine need for substitution.

---

## 6. When to split a provider into low-level API client + domain adapter

The default is to keep the provider HTTP client, wire types, and conversion together:

```text
streeteasy/
    client.go
    wire.go
    convert.go
```

Split only when there is a real reason.

For example:

```text
streeteasyapi/
    client.go
    types.go

streeteasy/
    source.go
    convert.go
```

is appropriate when the raw StreetEasy API client is independently valuable, such as when:

- multiple application adapters need raw StreetEasy objects,
- the raw API client is a reusable library,
- some callers need provider-specific fields that should not enter the domain,
- the provider protocol is large enough to be a cohesive package itself.

Then:

```text
streeteasyapi.Property
        |
        v
streeteasy adapter
        |
        v
domain.Property
```

### Rule

**Do not split this preemptively.**

A provider package containing transport + provider DTO + normalization is often the cleanest abstraction for an application.

---

## 7. Conversion rules

Conversions live with the boundary whose representation is being translated.

### External provider -> domain

Lives in the provider adapter:

```text
streeteasy/convert.go
```

### Persistence row <-> domain

Lives in persistence:

```text
store/convert.go
```

### Domain/application result -> HTTP JSON

Lives in HTTP transport:

```text
httpapi/convert.go
```

### Domain/application result -> protobuf

Lives in the gRPC/proto-facing adapter:

```text
grpcapi/convert.go
```

### Avoid a global converter package

Do not create:

```text
converters/
    streeteasy.go
    postgres.go
    http.go
    proto.go
```

unless conversion itself has somehow become a coherent product-level abstraction, which is uncommon.

Conversions are intentionally allowed to be repetitive. Mapping five fields explicitly is often cheaper to maintain than inventing a generic mapper.

---

## 8. Application/use-case layer

The application package owns workflows and orchestration.

Example:

```go
package listing

type PropertySource interface {
	Search(ctx context.Context, query SearchQuery) ([]domain.Property, error)
}

type PropertyStore interface {
	UpsertProperties(ctx context.Context, properties []domain.Property) error
}

type Service struct {
	source PropertySource
	store  PropertyStore
}

func NewService(source PropertySource, store PropertyStore) *Service {
	return &Service{
		source: source,
		store:  store,
	}
}

func (s *Service) Refresh(
	ctx context.Context,
	query SearchQuery,
) ([]domain.Property, error) {
	properties, err := s.source.Search(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("search properties: %w", err)
	}

	if err := s.store.UpsertProperties(ctx, properties); err != nil {
		return nil, fmt.Errorf("store properties: %w", err)
	}

	return properties, nil
}
```

### Application rules

Application services:

- **MAY** depend on domain types.
- **MAY** depend on small consumer-owned interfaces.
- **MUST NOT** know StreetEasy JSON details.
- **MUST NOT** know SQL column details.
- **MUST NOT** know HTTP response formatting.
- **SHOULD** contain business workflows, ranking, filtering, triage, orchestration, and policy.
- **SHOULD NOT** become a collection of one-line forwarding methods.

If a “service” contains no application behavior, it may not need to exist.

---

## 9. Store / persistence layer

The persistence package is a concrete implementation boundary.

The application talks in domain/application concepts:

```go
type PropertyStore interface {
	GetProperty(
		ctx context.Context,
		id domain.PropertyID,
	) (domain.Property, error)

	UpsertProperties(
		ctx context.Context,
		properties []domain.Property,
	) error
}
```

The concrete store can accept and return domain values:

```go
package store

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}
```

### Persistence owns persistence models

The store **MUST** own DB-specific representation.

Example:

```go
package store

type propertyRow struct {
	ID          string         `db:"id"`
	Address     string         `db:"address"`
	PriceCents  int64          `db:"price_cents"`
	Bedrooms    sql.NullInt64  `db:"bedrooms"`
	RawPayload  []byte         `db:"raw_payload"`
	ObservedAt  time.Time      `db:"observed_at"`
}
```

Then:

```go
func propertyRowFromDomain(p domain.Property) propertyRow {
	// Domain -> DB representation.
}

func (r propertyRow) toDomain() (domain.Property, error) {
	// DB representation -> domain.
}
```

### Store rules

- Domain structs **MUST NOT** carry `db` tags just to satisfy persistence.
- Store-private structs **MAY** use `db`, SQL, ORM, driver, nullable, serialization, and schema-specific types.
- Store internals **MAY** reflect table layout exactly.
- Store public methods **SHOULD** expose application/domain concepts rather than raw rows.
- SQL query details **MUST NOT** leak into application packages.
- Driver errors **SHOULD** be translated or wrapped before crossing the persistence boundary when callers need stable semantics.

Example:

```go
var ErrPropertyNotFound = errors.New("property not found")
```

Use stable sentinel/typed errors only when callers are expected to branch on them.

---

## 10. Transactions

Do not expose raw transaction machinery throughout the application unless workflows genuinely require it.

Prefer persistence-owned helpers such as:

```go
func (s *Store) InTx(
	ctx context.Context,
	fn func(*Store) error,
) error
```

or a narrowly designed consumer capability.

The application may control the **transaction boundary** when the business operation requires atomicity, but it should not become coupled to `pgx.Tx`, `sql.Tx`, or ORM-specific transaction types.

---

## 11. Outbound HTTP / JSON / protobuf / MCP

Transport-owned DTOs are not domain objects.

Example:

```go
package httpapi

type PropertyResponse struct {
	ID        string  `json:"id"`
	Address   string  `json:"address"`
	PriceUSD  float64 `json:"price_usd"`
	Bedrooms  int     `json:"bedrooms"`
}

func propertyResponse(p domain.Property) PropertyResponse {
	return PropertyResponse{
		ID:       string(p.ID),
		Address:  p.Address.String(),
		PriceUSD: p.Price.Dollars(),
		Bedrooms: p.Bedrooms,
	}
}
```

### Transport rules

- JSON tags belong to JSON-facing types.
- protobuf concerns belong to protobuf/generated types.
- MCP-specific request/response models belong to the MCP adapter.
- Domain types **MUST NOT** be changed merely because an external API contract changes.
- HTTP/MCP/gRPC handlers **SHOULD** be thin:
  1. decode/validate transport input,
  2. call application behavior,
  3. map errors,
  4. convert result,
  5. encode response.

### `api/` directory

A repository-level directory named `api/` is acceptable for contract assets:

```text
api/
    proto/
    openapi/
```

Avoid a broad Go package named `api` containing unrelated DTOs from every transport.

---

## 12. `main` is intentionally boring

`main` is the composition root.

It should:

1. load configuration,
2. validate configuration,
3. construct infrastructure,
4. construct adapters,
5. construct application services,
6. construct transports,
7. start servers/workers,
8. coordinate graceful shutdown.

Example:

```go
func main() {
	ctx := signalContext()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	propertyStore := store.New(db)

	streetEasy := streeteasy.NewClient(
		http.DefaultClient,
		streeteasy.Config{
			BaseURL: cfg.StreetEasyBaseURL,
		},
	)

	listings := listing.NewService(
		streetEasy,
		propertyStore,
	)

	mcpServer := mcp.NewServer(listings)

	if err := mcpServer.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
```

### `main` MUST NOT

- contain business rules,
- contain SQL,
- parse provider payloads,
- perform domain conversion,
- hide dependencies in global variables,
- act as a service locator.

A slightly repetitive `main` is good. Dependency wiring should be visible.

---

## 13. Dependency injection

Use explicit constructor injection.

Good:

```go
func NewService(source PropertySource, store PropertyStore) *Service
```

Avoid:

```go
var globalStore *store.Store
```

Avoid hidden package registries and service locators.

Dependencies that affect behavior **SHOULD** be visible in constructors or method arguments.

---

## 14. Context

For request-scoped work:

```go
func (c *Client) Search(
	ctx context.Context,
	query listing.SearchQuery,
) ([]domain.Property, error)
```

Rules:

- `context.Context` **SHOULD** be the first parameter.
- Context **MUST NOT** normally be stored in structs.
- Context **MUST** propagate through network, DB, subprocess, and other cancellable I/O.
- Library/application code **SHOULD NOT** replace a caller context with `context.Background()`.
- Timeouts belong near the boundary that understands the timeout policy.

---

## 15. Errors

Wrap errors with useful operation context:

```go
resp, err := c.http.Do(req)
if err != nil {
	return nil, fmt.Errorf("request StreetEasy search: %w", err)
}
```

Rules:

- Handle returned errors.
- Use `%w` when preserving the underlying error is useful.
- Error strings should normally begin lowercase and have no trailing punctuation.
- Create sentinel or typed errors only when callers need programmatic behavior.
- Do not expose raw provider/driver errors as your application contract when callers require stable semantics.
- Do not log and return the same error at every layer; normally log at the boundary responsible for the request/job.

---

## 16. Interfaces

### Default rule

> Interfaces belong to the consumer.

Example:

```go
package listing

type PropertyStore interface {
	UpsertProperties(
		ctx context.Context,
		properties []domain.Property,
	) error
}
```

Implemented implicitly by:

```go
package store

type Store struct {
	// ...
}

func (s *Store) UpsertProperties(
	ctx context.Context,
	properties []domain.Property,
) error {
	// ...
}
```

### Do

- define the interface next to the code that needs it,
- include only methods actually consumed,
- keep it private if it need not be exported,
- return concrete producer types,
- compose small interfaces when useful.

### Do not

- create `interfaces/`,
- put every repository interface in a central package,
- make producers return interfaces solely to hide implementations,
- create an interface only to generate a mock,
- mirror a concrete implementation’s entire method set.

### Exception

A producer-owned interface is appropriate when the interface itself is the canonical protocol/product, e.g. an `io.Writer`-like contract or generated protocol boundary used by many unrelated consumers.

---

## 17. Mocking and Mockery

Mocks are a testing tool, not an architecture driver.

### Rule

When a **real consumer-owned interface already exists** and interaction isolation is valuable, use generated Mockery stubs rather than maintaining repetitive hand-written mocks.

Generated mocks **SHOULD** live in a `mocks/` subdirectory associated with the consumer package when that layout does not introduce import cycles.

Example:

```text
internal/listing/
    service.go
    source.go
    store.go
    mocks/
        mocks.go
```

### Important import-cycle rule

A generated subpackage such as:

```text
listing/mocks
```

may need to import `listing` when interface method signatures use types declared by `listing`.

Therefore tests importing `listing/mocks` **SHOULD normally use**:

```go
package listing_test
```

rather than:

```go
package listing
```

This keeps the dependency graph:

```text
listing_test
    -> listing
    -> dependencies

listing_test
    -> listing/mocks
    -> listing
```

instead of producing:

```text
listing
    -> listing/mocks
    -> listing
```

For white-box tests that require unexported identifiers, prefer:

- a small hand-written fake,
- testing a narrower unit without generated mocks,
- or adjacent test-only generation if truly justified.

Do not contort production package structure merely to make mocking convenient.

### Prefer state tests when sufficient

If behavior can be tested naturally through outputs/state with a real lightweight implementation or fake, that may be better than asserting every interaction.

Use mocks when interaction behavior itself matters or real dependencies are expensive/non-deterministic.

---

## 18. Mockery v3 configuration

Pin the Mockery version used by the repository.

For Go 1.24+, prefer a `tool` dependency:

```bash
go get -tool github.com/vektra/mockery/v3@v3.7.3
```

Then invoke it through the module:

```bash
go tool mockery
```

Example `.mockery.yml`:

```yaml
template: testify

template-data:
  with-expecter: true

formatter: goimports
force-file-write: true

dir: '{{.InterfaceDir}}/mocks'
filename: mocks.go
pkgname: mocks

packages:
  example.com/project/internal/listing:
    interfaces:
      PropertySource:
      PropertyStore:
```

Prefer explicitly listing interfaces instead of recursively generating mocks for every interface in the repository.

That keeps “has an interface” separate from “needs a generated mock.”

---

## 19. Makefile targets

The repository **SHOULD** provide one obvious command for generated mocks.

Example:

```make
.PHONY: mocks
mocks:
	go tool mockery

.PHONY: generate
generate: mocks
	go generate ./...

.PHONY: test
test:
	go test ./...

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: check-generated
check-generated:
	$(MAKE) mocks
	git diff --exit-code -- .
```

If generation grows later, `make gen-code` remains the canonical regeneration entry point.

CI **SHOULD** fail if committed generated code is stale.

---

## 20. Constructors

Constructors should establish valid dependencies and make initialization obvious.

Prefer:

```go
func NewClient(httpClient *http.Client, cfg Config) *Client
func New(db *pgxpool.Pool) *Store
func NewService(source PropertySource, store PropertyStore) *Service
```

Avoid parameterless constructors followed by mutation:

```go
s := NewService()
s.SetStore(store)
s.SetSource(source)
```

unless runtime reconfiguration is genuinely required.

Constructor parameters should be required dependencies. Optional behavior may use a config struct or narrowly justified options.

---

## 21. Configuration

Configuration is infrastructure, not domain.

A configuration package may parse:

- environment variables,
- flags,
- config files.

It should then produce a validated concrete config used by `main`.

Bad:

```go
func (s *Service) Refresh(...) {
	apiKey := os.Getenv("STREETEASY_API_KEY")
}
```

Good:

```go
streetEasy := streeteasy.NewClient(httpClient, streeteasy.Config{
	APIKey: cfg.StreetEasyAPIKey,
})
```

Application/domain packages should not query environment variables directly.

---

## 22. Globals

Avoid mutable global state.

Dependencies such as:

- DB pools,
- HTTP clients,
- stores,
- loggers,
- service clients,
- caches,

should normally be concrete instances passed explicitly.

Package-level constants and immutable values are fine.

A package-level convenience API is acceptable only when it is a thin wrapper over an instance-based API and does not undermine isolation/testability.

---

## 23. HTTP clients

Provider adapters should normally accept an HTTP client:

```go
func NewClient(httpClient *http.Client, cfg Config) *Client
```

Do not create a fresh `http.Client` for every request.

Provider-specific concerns such as:

- endpoints,
- authentication,
- headers,
- pagination,
- retries,
- provider rate limiting,
- provider response decoding,

belong in the provider package.

Application logic should not construct provider URLs.

---

## 24. Logging

Prefer structured logging at meaningful boundaries.

Application functions should return errors rather than logging every failure.

Avoid this:

```go
if err != nil {
	logger.Error("failed", "err", err)
	return fmt.Errorf("failed: %w", err)
}
```

at every layer.

This creates duplicate logs.

A common model is:

```text
low-level layer: wrap
application: wrap / classify
request/job boundary: log once
```

Log fields should be structured rather than embedded into formatted strings when supported.

---

## 25. Package naming

Good package names describe what they provide at call sites.

Good:

```text
domain
listing
streeteasy
store
httpapi
mcp
```

Usually bad:

```text
utils
helpers
common
types
interfaces
converters
managers
getters
fetchers
```

The question to ask is:

> What cohesive capability does this package provide to its caller?

If the answer is “miscellaneous functions related to X,” the boundary is probably weak.

### Exception: `internal/shared/<name>`

Cross-cutting generic helpers used by multiple packages MAY live under
`internal/shared/`, provided each subpackage is narrowly named and scoped by
capability, not by “miscellaneous”:

```text
internal/shared/ptr        ptr.To, ptr.Deref, ptr.Clone, ptr.First
internal/shared/xslices    xslices.Map
```

The ban above is on *generically named, unscoped* packages (`utils`, `helpers`,
`common`), not on sharing itself. A helper is promoted into `internal/shared`
when it is (or is about to be) reimplemented in a second package; a helper used
by exactly one package stays private to it. `internal/shared` packages MUST NOT
import domain, provider, store, or transport packages.

---

## 26. Files are not architecture boundaries

Multiple `.go` files in the same package are an organizational tool only.

This is good:

```text
streeteasy/
    client.go
    wire.go
    convert.go
    pagination.go
```

All may remain `package streeteasy`.

Do not create a package for every file-level concept.

Split a package only when the resulting package has a coherent API and can stand on its own.

---

## 27. Visibility

Default to unexported.

Export an identifier only when another package genuinely needs it.

Example:

```go
type propertyResponse struct { ... } // provider implementation detail
```

not:

```go
type StreetEasyAPIPropertyResponse struct { ... }
```

unless external packages really consume that type.

The smaller the package API, the easier the codebase is to refactor.

---

## 28. Testing strategy

### Application packages

Use:

- table-driven tests for pure policy/ranking/filtering,
- Mockery-generated mocks for genuine consumer interfaces when interaction isolation is valuable,
- fakes when a lightweight working implementation makes tests clearer.

### Provider packages

Test:

- response decoding,
- conversion,
- pagination,
- errors,
- HTTP behavior.

Prefer `httptest.Server` or a custom `RoundTripper` over inventing a large interface around `http.Client`.

### Store packages

Test SQL and persistence semantics against the real DB engine when practical.

Store conversion helpers may be unit tested directly.

Do not mock SQL so heavily that tests only confirm the mock configuration.

### Transport packages

Test:

- decoding,
- validation,
- status/error mapping,
- serialization,
- calls into the application capability.

---

## 29. Generated code

Generated code should be deterministic and reproducible.

Generated files:

- **SHOULD** clearly state that they are generated,
- **SHOULD NOT** be manually edited,
- **SHOULD** be regenerated through `make generate` or the repository's canonical target,
- **SHOULD** use a pinned generator version,
- **SHOULD** be checked by CI when committed.

---

## 30. Concurrency

Keep concurrency ownership obvious.

- The component starting a goroutine is responsible for its lifetime.
- Goroutines should have a clear shutdown mechanism.
- Prefer synchronous APIs unless asynchronous behavior is intrinsic.
- Propagate cancellation through context.
- Do not hide background goroutines in constructors without a documented lifecycle.
- If a component needs `Close`, make ownership of that call clear in `main` or its parent component.

---

## 31. Data ownership and mutation

Prefer clear ownership.

If a function retains a slice, map, or pointer supplied by its caller, document it or defensively copy when necessary.

Avoid sharing mutable domain objects across goroutines without synchronization.

Do not expose internal store/provider buffers merely to save a copy unless profiling demonstrates that the copy matters.

---

## 32. Avoid abstraction leakage

These should trigger review concern:

```go
domain.Property{
	Price: sql.NullInt64{...},
}
```

```go
listing.RefreshRequest{
	StreetEasyPageSize: 50,
}
```

```go
store.GetProperty(...) (*streeteasy.PropertyResponse, error)
```

```go
httpapi.Handler{
	DB: *pgxpool.Pool,
}
```

They indicate one boundary has learned too much about another.

---

## 33. What may be shared across providers

Share stable domain/application concepts:

```go
domain.Property
listing.SearchQuery
listing.PropertySource
```

Do **not** force provider-specific wire data into a “universal API DTO.”

For example, if StreetEasy has:

```text
listing_id
building_slug
no_fee
```

and another provider has different semantics, each provider should model its own response accurately and normalize only the concepts the application actually needs.

Lossy normalization is fine when the domain intentionally does not care about provider-specific data.

If raw provider data needs preservation, store it separately as raw metadata rather than polluting the canonical domain model.

---

## 34. Raw provider payloads

It is reasonable for persistence to retain original provider payloads for:

- debugging,
- replay,
- future reprocessing,
- auditing provider changes.

For example:

```go
type propertyRow struct {
	// ...
	Provider   string `db:"provider"`
	RawPayload []byte `db:"raw_payload"`
}
```

But raw payload is a persistence/adapter concern. Application business logic should not parse random raw JSON from DB rows as its normal execution path.

---

## 35. Recommended final dependency graph

```text
cmd/app
  |
  +--> streeteasy
  |       |
  |       +--> domain
  |
  +--> store
  |       |
  |       +--> domain
  |
  +--> listing
  |       |
  |       +--> domain
  |
  +--> httpapi / mcp
          |
          +--> listing
          +--> domain
```

Conceptually:

```text
StreetEasy
    |
    v
streeteasy.Client
    |
    | satisfies listing.PropertySource
    v
listing.Service
    |
    | uses listing.PropertyStore
    v
store.Store
```

The concrete dependency wiring happens in `main`.

---

## 36. Architectural decision: getters/fetchers/converters

### Default ruling

**Do not introduce generic `getters`, `fetchers`, or `converters` packages.**

Instead:

```text
listing.PropertySource      consumer-owned capability
streeteasy.Client           provider implementation
streeteasy.propertyResponse provider wire model
streeteasy.convert.go       provider normalization
domain.Property             canonical application model
```

This gives a clean boundary without requiring a framework-like abstraction.

### Allow a shared source package only if the protocol itself becomes the product

If many packages need a canonical source protocol and duplicating it becomes harmful, a dedicated package may eventually be justified:

```text
propertysource/
```

But this is an exception.

The interface should be promoted only because it has become a shared protocol with stable semantics, not because several implementations happen to “fetch things.”

---

## 37. Review checklist for agents

Before completing a change, verify:

### Boundaries

- [ ] Does provider-specific data stay in the provider adapter?
- [ ] Does DB-specific representation stay in persistence?
- [ ] Does transport-specific representation stay in the transport?
- [ ] Are domain types free of external serialization/storage concerns?

### Interfaces

- [ ] Is each new interface owned by its consumer?
- [ ] Is the interface actually needed?
- [ ] Is it minimal?
- [ ] Does the producer still return a concrete type?
- [ ] Was no interface created merely to make mocking possible?

### Conversion

- [ ] Is conversion colocated with the boundary representation being translated?
- [ ] Was a generic converter/helper package avoided?
- [ ] Are mappings explicit and readable?

### Persistence

- [ ] Does the store accept/return domain/application concepts?
- [ ] Are DB tags/types confined to store-private models?
- [ ] Are DB implementation details hidden from application code?

### Testing

- [ ] If a consumer interface needs mocking, is the mock generated with Mockery?
- [ ] Is the generated mock in the package-associated `mocks/` folder where practical?
- [ ] Does the test package avoid an import cycle?
- [ ] Would a fake/real implementation produce a simpler test than a mock?

### Runtime

- [ ] Is `context.Context` propagated?
- [ ] Are errors wrapped meaningfully?
- [ ] Is logging performed at the appropriate boundary?
- [ ] Are goroutine lifetimes explicit?

### Composition

- [ ] Is dependency construction/wiring in `main`?
- [ ] Is `main` free of domain/business logic?
- [ ] Are dependencies explicit rather than global?

### Hygiene

- [ ] Are package names specific and meaningful?
- [ ] Are exports actually required?
- [ ] Is generated code reproducible?
- [ ] Does `go test ./...` pass?
- [ ] Does formatting pass?
- [ ] Is generated code current?

---

## 38. Rules for coding agents

When implementing code in this repository:

1. **Preserve existing architecture unless there is a concrete reason to change it.**
2. **Do not create new packages reflexively.**
3. **Do not add generic `utils`, `common`, `helpers`, `converters`, `interfaces`, `getters`, or `fetchers` packages.** Shared generic helpers go in narrowly named `internal/shared/<name>` subpackages (see the §25 exception, or the [architecture decisions](decisions.md)); check there before reimplementing a pointer/slice helper privately.
4. **Put provider API DTOs and provider-to-domain conversion in the provider adapter.**
5. **Use domain types between application-owned layers.**
6. **Put persistence DTO/row structs and DB tags inside the store package.**
7. **Put JSON/protobuf/MCP DTOs inside their transport boundary.**
8. **Define interfaces at the point of consumption.**
9. **Do not create an interface solely for a test.**
10. **When a real consumer interface benefits from mocking, use Mockery-generated mocks rather than hand-maintaining repetitive mocks.**
11. **Keep generated mocks associated with the consumer package and use `_test` package structure when necessary to avoid import cycles.**
12. **Return concrete implementation types from constructors unless returning an interface is substantively justified.**
13. **Pass `context.Context` explicitly through cancellable work.**
14. **Wrap errors with operation context.**
15. **Keep `main` limited to composition and lifecycle management.**
16. **Prefer explicit code over speculative generic abstractions.**
17. **Do not move a boundary concern into the domain merely to reduce mapping code.**
18. **Do not expose a new symbol/package merely because it might be useful later.**
19. **Add tests at the boundary where behavior lives.**
20. **Run formatting, generation, and tests before considering the change complete.**

---

## 39. Research basis

This guidance intentionally follows mainstream/current Go design advice while making a few project-specific choices.

Primary references:

- Go Blog: **Package names**  
  https://go.dev/blog/package-names

- Go Wiki: **Go Code Review Comments**  
  https://go.dev/wiki/CodeReviewComments

- Go: **Effective Go**  
  https://go.dev/doc/effective_go

- Go: **Organizing a Go module**  
  https://go.dev/doc/modules/layout

- Go Blog: **Contexts and structs**  
  https://go.dev/blog/context-and-structs

- Google Go Style: **Best Practices**  
  https://google.github.io/styleguide/go/best-practices

- Google Go Style: **Style Decisions**  
  https://google.github.io/styleguide/go/decisions

- Go: **Managing dependencies / tool dependencies**  
  https://go.dev/doc/modules/managing-dependencies

- Mockery v3: **Configuration**  
  https://vektra.github.io/mockery/v3.5/configuration/

- Mockery v3: **Migration/layout notes**  
  https://vektra.github.io/mockery/v3.4/v3/

- Mockery: **Releases**  
  https://github.com/vektra/mockery/releases

### Project-specific choices

The following are intentional conventions for this project rather than universal Go laws:

- provider-local conversion instead of a central converter package,
- a `mocks/` subpackage per consumer package where generated mocks are useful,
- a `store` package that externally deals in domain concepts but internally owns DB row models/tags,
- a top-level `api/` directory for schema assets while avoiding a generic Go package named `api`,
- a thin application/use-case package between transport/provider/persistence boundaries.

These choices should be changed only when the codebase demonstrates a concrete need, not to satisfy another architectural pattern mechanically.
