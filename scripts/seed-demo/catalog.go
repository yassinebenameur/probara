package main

import (
	"fmt"
	"strings"
	"time"
)

// Profile is how a target behaves. The same table drives the live mock
// (targets) and the synthetic history (seed), so the seam between backfilled
// and live checks is invisible.
type Profile string

const (
	Healthy   Profile = "healthy"   // 2xx, LatMin..LatMax
	Slow      Profile = "slow"      // 2xx, 400-1500ms
	Flaky     Profile = "flaky"     // ~6% 503
	Down      Profile = "down"      // 503 since DownAgo
	Refused   Profile = "refused"   // nothing listens (dead port) since DownAgo
	MaintDown Profile = "maintdown" // 503 while its active maintenance window runs
	Paused    Profile = "paused"    // enabled=false since PausedAgo (0 = since creation)
	External  Profile = "external"  // real internet target; history assumes healthy
)

// Ports are offsets from -mock-port.
const (
	offHTTP      = 0  // HTTP + websocket mock (dual-stack: [::1] and 127.0.0.1)
	offGRPC      = 2  // gRPC health server
	offTCP       = 10 // first raw TCP listener
	offSIP       = 60 // first SIP UDP responder
	offMesh      = 81 // first location-worker mesh echo port
	offMeshProxy = 84 // first mesh endpoint: the mock's per-location WAN delay in front of a worker's echo
	offMet       = 91 // first location-worker metrics port
	offDead      = 99 // never listened on
)

// Domain is the public loopback wildcard every target lives under:
// *.localtest.me resolves to 127.0.0.1 and ::1 in public DNS, so each
// service gets a real-looking hostname without touching /etc/hosts. Ports
// stay at 191xx (binding :80 needs root), and resolving them needs a
// working DNS resolver (offline, every check fails with a lookup error).
const Domain = "northwind.localtest.me"

// Env carries the runtime values the configs embed.
type Env struct {
	Base int
}

// fqdn is a service hostname under Domain; a label that already has a dot
// (a partner's own domain) is taken as a full name under localtest.me.
func (e Env) fqdn(label string) string {
	if strings.Contains(label, ".") {
		return label + ".localtest.me"
	}
	return label + "." + Domain
}
func (e Env) url(label, path string) string {
	return fmt.Sprintf("http://%s:%d%s", e.fqdn(label), e.Base+offHTTP, path)
}
func (e Env) port(off int) int { return e.Base + off }

// Spec is one monitor of the world.
type Spec struct {
	Key, Name, Type, Team string
	Host                  string // hostname label under Domain (default: hostLabels, else the key)
	Tags                  []string
	Interval, Timeout     int
	Threshold             int
	Profile               Profile
	LatMin, LatMax        int            // ms
	Path                  string         // mock path (http/ws); also the mock route key
	URL                   string         // explicit URL (third-party targets)
	Keyword               string         // HTML body keyword asserted by the monitor
	JSON                  map[string]any // JSON body served (and asserted status==ok)
	Display               [][2]string    // display_fields: {path,label}
	Config                map[string]any // explicit config (non-http types)
	DependsOn             []string
	Locations             []string // location keys
	Quorum                int
	Notify                string // "" | "custom-disabled" | "custom-empty"
	DownAgo               time.Duration
	PausedAgo             time.Duration
	OutageRate            float64 // random real outages per 30 days
	NoOutages             bool
	Members               []string // group members (keys)
	Rollup                string
	Port                  int // tcp/sip/grpc offset
	Service               string
	PushPayload           map[string]string
}

// LocationSpec is one private location. Worker locations get a real
// /tmp/probara-bin/worker started by `targets`.
type LocationSpec struct {
	Key, Name, Description string
	MeshOff                int // worker echo port offset; 0 = not in the mesh
	Worker                 bool
	ExtraEnv               []string
	Enabled                bool
	LastSeenAgo            time.Duration // for worker-less locations
	// OriginRTT is the network distance (ms) from this location to
	// Northwind's London origin, added to every check it runs, live (the
	// mock identifies the calling worker, see callerOf) and in history.
	OriginRTT int
}

// meshEndpoint is the host:port other locations probe. It is the mock's
// WAN-delay proxy in front of the worker's echo listener, so mesh probes
// carry realistic inter-region round trips (see meshRTT).
func (l LocationSpec) meshEndpoint(e Env) string {
	return fmt.Sprintf("%s.mesh.%s:%d", l.Key, Domain, e.port(l.MeshOff-offMesh+offMeshProxy))
}

func locationCatalog() []LocationSpec {
	return []LocationSpec{
		{Key: "fra", Name: "Frankfurt (eu-central-1)", Description: "AWS eu-central-1 · probe VPC", MeshOff: offMesh, Worker: true, Enabled: true, OriginRTT: 18},
		{Key: "iad", Name: "Virginia (us-east-1)", Description: "AWS us-east-1 · probe VPC", MeshOff: offMesh + 1, Worker: true, Enabled: true, OriginRTT: 85},
		// Singapore's strict egress policy (127/8 only) also makes it dial
		// over IPv4 while the others use ::1: the fallback that still tells
		// it apart when lsof is unavailable (see callerOf).
		{Key: "sin", Name: "Singapore (ap-southeast-1)", Description: "AWS ap-southeast-1 · strict egress policy", MeshOff: offMesh + 2, Worker: true, Enabled: true, OriginRTT: 160,
			ExtraEnv: []string{"HTTP_BLOCK_PRIVATE_IPS=true", "HTTP_ALLOWED_CIDRS=127.0.0.0/8"}},
		{Key: "gru", Name: "São Paulo (sa-east-1)", Description: "Pilot PoP — worker host decommissioned", Enabled: true, LastSeenAgo: 52 * time.Hour, OriginRTT: 190},
		{Key: "nrt", Name: "Tokyo lab", Description: "Lab probe, disabled until Q1", Enabled: false, LastSeenAgo: 19 * 24 * time.Hour, OriginRTT: 230},
	}
}

func locationByKey(key string) (LocationSpec, bool) {
	for _, l := range locationCatalog() {
		if l.Key == key {
			return l, true
		}
	}
	return LocationSpec{}, false
}

// meshRTT is the round trip (ms) between two regions, roughly what
// eu-central-1 / us-east-1 / ap-southeast-1 measure on the public backbone.
func meshRTT(a, b string) int {
	if a > b {
		a, b = b, a
	}
	switch a + "-" + b {
	case "fra-iad":
		return 89
	case "fra-sin":
		return 158
	case "iad-sin":
		return 214
	}
	return 1
}

// sinEdgeHosts are served through the CDN whose Singapore PoP is struggling:
// checks of them from Singapore get a slow 504 (live and in history), so the
// global monitors on them are degraded.
var sinEdgeHosts = map[string]bool{"www": true, "track": true}

// Teams double as dashboard group tags.
var teams = []string{"storefront", "checkout", "payments", "search", "logistics", "identity", "data", "platform", "voice"}

func web(key, name, team, path, keyword string, s Spec) Spec {
	s.Key, s.Name, s.Type, s.Team, s.Path, s.Keyword = key, name, "http", team, path, keyword
	return s
}

func api(key, name, team, path string, s Spec) Spec {
	s.Key, s.Name, s.Type, s.Team, s.Path = key, name, "http", team, path
	if s.JSON == nil {
		s.JSON = map[string]any{}
	}
	return s
}

func tcp(key, name, team string, off int, s Spec) Spec {
	s.Key, s.Name, s.Type, s.Team, s.Port = key, name, "tcp", team, off
	return s
}

func grpcS(key, name, team, svc string, s Spec) Spec {
	s.Key, s.Name, s.Type, s.Team, s.Port, s.Service = key, name, "grpc", team, offGRPC, svc
	return s
}

func wsS(key, name, team, path string, s Spec) Spec {
	s.Key, s.Name, s.Type, s.Team, s.Path = key, name, "websocket", team, path
	return s
}

func sip(key, name string, off int, s Spec) Spec {
	s.Key, s.Name, s.Type, s.Team, s.Port = key, name, "sip", "voice", off
	return s
}

func dns(key, name, team, host, rtype, ns string, s Spec) Spec {
	s.Key, s.Name, s.Type, s.Team = key, name, "dns", team
	s.Config = map[string]any{"host": host, "record_type": rtype, "nameserver": ns}
	if s.Interval == 0 {
		s.Interval = 120
	}
	if s.LatMin == 0 {
		s.LatMin, s.LatMax = 16, 42
	}
	return s
}

func push(key, name, team string, interval, grace int, s Spec) Spec {
	s.Key, s.Name, s.Type, s.Team, s.Interval = key, name, "push", team, interval
	s.Config = map[string]any{"expected_interval_seconds": interval, "grace_period_seconds": grace}
	return s
}

func pg(e Env, key, name, team, host, query string, s Spec) Spec {
	s.Key, s.Name, s.Type, s.Team, s.Host = key, name, "postgres", team, host
	cfg := map[string]any{"host": e.fqdn(host), "port": 5432, "database": "probara", "username": "probara",
		"password": "probara", "ssl_mode": "disable", "query": query, "warn_latency_ms": 250, "max_latency_ms": 3000}
	for k, v := range s.Config {
		cfg[k] = v
	}
	s.Config = cfg
	if s.LatMin == 0 {
		s.LatMin, s.LatMax = 15, 26
	}
	return s
}

func group(key, name, team string, members []string, s Spec) Spec {
	s.Key, s.Name, s.Type, s.Team, s.Members = key, name, "group", team, members
	return s
}

// catalog is the whole world. Keys are stable references; names are what the
// UI shows and must be unique (targets maps DB rows back to specs by name).
func catalog(e Env) []Spec {
	h := Spec{}
	slow := Spec{Profile: Slow}
	flaky := Spec{Profile: Flaky, Threshold: 3, OutageRate: 0.3}
	glob := []string{"fra", "iad", "sin"}

	specs := []Spec{
		// ── storefront ────────────────────────────────────────────────
		web("sf-home", "Storefront — Home", "storefront", "/storefront/home", "Free delivery over £50", Spec{Interval: 30, Tags: []string{"tier-1", "customer-facing"}, LatMin: 45, LatMax: 95}),
		web("sf-category", "Storefront — Category: Kitchen", "storefront", "/storefront/c/kitchen", "Kitchen and dining", h),
		web("sf-pdp", "Storefront — Product page", "storefront", "/storefront/p/nw-48213", "Add to basket", Spec{Tags: []string{"tier-1", "customer-facing"}, DependsOn: []string{"product-api", "pricing-api"}}),
		web("sf-search", "Storefront — Search results", "storefront", "/storefront/search", "results for", Spec{DependsOn: []string{"search-api"}}),
		web("sf-basket", "Storefront — Basket page", "storefront", "/storefront/basket", "Your basket", Spec{DependsOn: []string{"cart-api"}}),
		web("sf-account", "Storefront — My account", "storefront", "/storefront/account", "Your orders", h),
		web("sf-stores", "Store locator", "storefront", "/storefront/stores", "Find a store", Spec{Profile: Slow, LatMin: 650, LatMax: 1400}),
		web("sf-journal", "Northwind Journal (blog)", "storefront", "/journal", "Northwind Journal", Spec{Interval: 120}),
		web("sf-sitemap", "Sitemap.xml", "storefront", "/sitemap.xml", "<urlset", Spec{Interval: 300}),
		web("sf-robots", "robots.txt", "storefront", "/robots.txt", "Sitemap:", Spec{Interval: 300}),
		web("sf-cdn", "CDN — static bundle (app.js)", "storefront", "/static/app.4f9c2.js", "webpackChunk", Spec{LatMin: 12, LatMax: 30}),
		api("sf-images", "Image resizer", "storefront", "/img/resize/health", Spec{JSON: map[string]any{"cache_hit_ratio": 0.94}}),
		api("product-api", "Product catalog API", "storefront", "/api/catalog/v2/health", Spec{Tags: []string{"tier-1"}, JSON: map[string]any{"version": "2.31.4", "sku_count": 48213, "db": "ok"},
			Display: [][2]string{{"version", "Version"}, {"sku_count", "SKUs"}}, DependsOn: []string{"orders-db-replica"}}),
		api("pricing-api", "Pricing API", "storefront", "/api/pricing/health", Spec{JSON: map[string]any{"version": "1.18.0", "rules_loaded": 1294}, Display: [][2]string{{"rules_loaded", "Rules"}}}),
		api("reviews-api", "Reviews API", "storefront", "/api/reviews/health", flaky),
		api("recs-api", "Recommendations API", "storefront", "/api/recs/health", Spec{Profile: Slow, LatMin: 450, LatMax: 1100, JSON: map[string]any{"model": "two-tower-v7"}, Display: [][2]string{{"model", "Model"}}}),
		api("cms-api", "CMS proxy (Contentful)", "storefront", "/api/cms/health", h),
		api("giftcards", "Gift cards API", "storefront", "/api/giftcards/health", h),
		web("sf-bf-queue", "Black Friday waiting room", "storefront", "/queue", "You are in the queue", Spec{Profile: Paused, PausedAgo: 26 * 24 * time.Hour, Tags: []string{"seasonal"}}),
		web("sf-v1", "Storefront v1 (sunset)", "storefront", "/v1/home", "Northwind", Spec{Profile: Paused, PausedAgo: 21 * 24 * time.Hour, Tags: []string{"deprecated"}}),

		// ── checkout ──────────────────────────────────────────────────
		web("checkout-web", "Checkout — page", "checkout", "/checkout", "Secure checkout", Spec{Interval: 30, Tags: []string{"tier-1", "customer-facing"}, NoOutages: true, DependsOn: []string{"checkout-api"}}),
		api("checkout-api", "Checkout API", "checkout", "/api/checkout/health", Spec{Interval: 30, Tags: []string{"tier-1"}, NoOutages: true,
			JSON: map[string]any{"version": "5.4.2", "db_pool_in_use": 14, "region": "eu-west-2"}, Display: [][2]string{{"version", "Version"}, {"db_pool_in_use", "DB pool"}},
			DependsOn: []string{"orders-db", "payments-api", "cart-redis", "inventory-grpc"}}),
		api("cart-api", "Cart API", "checkout", "/api/cart/health", Spec{NoOutages: true, DependsOn: []string{"cart-redis"}}),
		{Key: "cart-redis", Name: "Cart cache (Redis)", Type: "redis", Team: "checkout", LatMin: 4, LatMax: 9,
			Host: "cart-redis-01", Config: map[string]any{"host": e.fqdn("cart-redis-01"), "port": 6379, "db": 0, "expected_role": "master", "warn_latency_ms": 50, "max_latency_ms": 1000}},
		api("promo-api", "Promotions engine", "checkout", "/api/promotions/health", Spec{JSON: map[string]any{"active_campaigns": 7}, Display: [][2]string{{"active_campaigns", "Campaigns"}}}),
		api("tax-api", "Tax calculation (Avalara proxy)", "checkout", "/api/tax/health", Spec{Profile: Slow, LatMin: 420, LatMax: 900}),
		api("address-api", "Address lookup (Loqate proxy)", "checkout", "/api/address/health", h),
		{Key: "checkout-journey", Name: "Checkout API journey", Type: "synthetic_api", Team: "checkout", Interval: 120, Timeout: 30, LatMin: 90, LatMax: 220, NoOutages: true,
			Config: map[string]any{"base_url": "http://" + e.fqdn("checkout-api") + fmt.Sprintf(":%d", e.port(offHTTP)), "steps": []any{
				map[string]any{"id": "cart", "request": map[string]any{"method": "GET", "url": "/api/cart/health"}, "assert": []any{map[string]any{"target": "status", "op": "equals", "value": 200}}},
				map[string]any{"id": "session", "request": map[string]any{"method": "POST", "url": "/api/checkout/session", "body": `{"basket":"b_7f21"}`, "headers": map[string]any{"Content-Type": "application/json"}},
					"assert":  []any{map[string]any{"target": "status", "op": "equals", "value": 201}},
					"extract": []any{map[string]any{"name": "sid", "from": "json", "path": "session_id"}}},
				map[string]any{"id": "confirm", "request": map[string]any{"method": "GET", "url": "/api/checkout/session/{{sid}}"}, "assert": []any{map[string]any{"target": "json", "op": "equals", "path": "state", "value": "ready"}}},
			}}},
		api("orders-api", "Orders API", "checkout", "/api/orders/health", Spec{Tags: []string{"tier-1"}, JSON: map[string]any{"version": "3.9.0", "queue_lag": 0}, Display: [][2]string{{"queue_lag", "Queue lag"}},
			DependsOn: []string{"orders-db", "events-mq"}}),
		web("order-history", "Order history page", "checkout", "/storefront/account/orders", "Order history", Spec{DependsOn: []string{"orders-api"}}),
		grpcS("inventory-grpc", "Inventory service (gRPC)", "checkout", "northwind.inventory.v1.Inventory", Spec{}),
		wsS("basket-ws", "Live basket sync (WebSocket)", "checkout", "/ws/basket", Spec{LatMin: 33, LatMax: 60}),

		// ── payments ──────────────────────────────────────────────────
		api("payments-api", "Payments API", "payments", "/api/payments/health", Spec{Interval: 30, Tags: []string{"tier-1", "pci"}, NoOutages: true,
			JSON: map[string]any{"version": "7.2.1", "psp_primary": "adyen"}, Display: [][2]string{{"psp_primary", "Primary PSP"}}, DependsOn: []string{"fraud-grpc", "payments-db"}}),
		api("payment-auth", "Card authorisation (Adyen)", "payments", "/api/payments/psp/adyen/health", Spec{Interval: 30, Tags: []string{"tier-1", "pci"}, NoOutages: true, DependsOn: []string{"payments-api"}}),
		api("payment-paypal", "PayPal Express", "payments", "/api/payments/psp/paypal/health", Spec{Tags: []string{"pci"}}),
		api("payment-klarna", "Klarna pay-later", "payments", "/api/payments/psp/klarna/health", flaky),
		api("payment-capture", "Capture & settlement worker", "payments", "/api/payments/capture/health", Spec{JSON: map[string]any{"pending_captures": 3}, Display: [][2]string{{"pending_captures", "Pending"}}}),
		api("payment-refunds", "Refunds API", "payments", "/api/payments/refunds/health", h),
		api("payments-3ds", "3-D Secure challenge", "payments", "/api/payments/3ds/health", Spec{Tags: []string{"pci"}, NoOutages: true}),
		api("payments-webhooks", "PSP webhook receiver", "payments", "/webhooks/psp/health", h),
		grpcS("fraud-grpc", "Fraud scoring (gRPC)", "payments", "northwind.fraud.v1.Scoring", Spec{}),
		pg(e, "payments-db", "Payments ledger DB (Postgres)", "payments", "payments-db-01", "SELECT 1", Spec{Tags: []string{"pci"}}),
		dns("dns-adyen", "DNS — adyen.com", "payments", "adyen.com", "A", "1.1.1.1", h),
		dns("dns-stripe", "DNS — api.stripe.com", "payments", "api.stripe.com", "A", "8.8.8.8", h),

		// ── search ────────────────────────────────────────────────────
		api("search-api", "Search API", "search", "/api/search/health", Spec{Tags: []string{"tier-1"}, JSON: map[string]any{"index_version": "v8-2026-10-02", "docs": 1844120},
			Display: [][2]string{{"index_version", "Index"}, {"docs", "Docs"}}, DependsOn: []string{"search-node-1", "search-qu"}}),
		api("search-suggest", "Autocomplete", "search", "/api/search/suggest", Spec{LatMin: 18, LatMax: 40, DependsOn: []string{"search-api"}}),
		tcp("search-node-1", "Search cluster — node 1", "search", offTCP+0, Spec{}),
		tcp("search-node-2", "Search cluster — node 2", "search", offTCP+1, Spec{}),
		tcp("search-node-3", "Search cluster — node 3", "search", offTCP+2, Spec{}),
		api("search-indexer", "Search indexer admin", "search", "/api/search/indexer/health", Spec{Profile: MaintDown}),
		push("search-sync", "Catalog → search sync", "search", 300, 120, Spec{PushPayload: map[string]string{"docs_updated": "1200"}}),
		api("search-synonyms", "Synonyms service", "search", "/api/search/synonyms/health", h),
		api("search-ranking", "Ranking model server", "search", "/api/search/ranking/health", Spec{Profile: Slow, LatMin: 500, LatMax: 1200}),
		grpcS("search-qu", "Query understanding (gRPC)", "search", "northwind.search.v1.QueryUnderstanding", Spec{}),

		// ── logistics ─────────────────────────────────────────────────
		web("track-page", "Track my order page", "logistics", "/track", "Track your parcel", Spec{Tags: []string{"customer-facing"}, DependsOn: []string{"tracking-api"}}),
		api("tracking-api", "Tracking API", "logistics", "/api/tracking/health", Spec{Tags: []string{"tier-1"}, DependsOn: []string{"carrier-dpd"}}),
		api("carrier-dhl", "Carrier webhook relay — DHL Express", "logistics", "/api/carriers/dhl/relay/health", Spec{Profile: Down, DownAgo: 26 * time.Minute, Tags: []string{"carrier"}}),
		api("carrier-dpd", "Carrier API — DPD", "logistics", "/api/carriers/dpd/health", Spec{Tags: []string{"carrier"}}),
		api("carrier-ups", "Carrier API — UPS", "logistics", "/api/carriers/ups/health", Spec{Profile: Flaky, Threshold: 3, OutageRate: 0.3, Tags: []string{"carrier"}}),
		api("carrier-royalmail", "Carrier API — Royal Mail", "logistics", "/api/carriers/royalmail/health", Spec{Tags: []string{"carrier"}}),
		api("carrier-evri", "Carrier API — Evri", "logistics", "/api/carriers/evri/health", Spec{Profile: Slow, LatMin: 600, LatMax: 1500, Tags: []string{"carrier"}}),
		web("returns", "Returns & exchanges portal", "logistics", "/returns", "Start a return", Spec{Profile: Down, DownAgo: 2*time.Hour + 13*time.Minute, Tags: []string{"customer-facing"}, DependsOn: []string{"orders-api"}}),
		api("wms-leeds", "WMS — Leeds DC", "logistics", "/api/wms/leeds/health", Spec{JSON: map[string]any{"picks_per_hour": 1840}, Display: [][2]string{{"picks_per_hour", "Picks/h"}}}),
		api("wms-daventry", "WMS — Daventry DC", "logistics", "/api/wms/daventry/health", Spec{JSON: map[string]any{"picks_per_hour": 2210}, Display: [][2]string{{"picks_per_hour", "Picks/h"}}}),
		api("label-svc", "Label service", "logistics", "/api/labels/health", h),
		grpcS("route-grpc", "Route optimiser (gRPC)", "logistics", "northwind.routing.v1.Optimizer", Spec{}),
		wsS("dispatch-ws", "Driver dispatch feed (WebSocket)", "logistics", "/ws/dispatch", Spec{LatMin: 40, LatMax: 75}),
		tcp("sftp-meridian", "Partner SFTP — Meridian Wholesale", "logistics", offDead, Spec{Profile: Refused, DownAgo: 3*24*time.Hour + 4*time.Hour, Tags: []string{"partner", "edi"}, Interval: 120}),
		tcp("edi-as2", "EDI AS2 gateway", "logistics", offTCP+3, Spec{Tags: []string{"partner", "edi"}}),
		push("label-printer", "Label printer bridge — Leeds DC", "logistics", 120, 60, h),
		push("carrier-manifest", "Nightly carrier manifest upload", "logistics", 86400, 3600, Spec{PushPayload: map[string]string{"parcels": "18233"}}),
		push("stock-sync", "Stock sync cron (5 min)", "logistics", 300, 120, Spec{PushPayload: map[string]string{"skus": "48213"}}),
		dns("dns-dhl", "DNS — api-eu.dhl.com", "logistics", "api-eu.dhl.com", "A", "1.1.1.1", h),

		// ── identity ──────────────────────────────────────────────────
		web("login-page", "Sign-in page", "identity", "/account/login", "Sign in to Northwind", Spec{Tags: []string{"customer-facing"}, DependsOn: []string{"identity-api"}}),
		api("identity-api", "Identity API", "identity", "/api/identity/health", Spec{Tags: []string{"tier-1"}, DependsOn: []string{"identity-db", "session-redis"}}),
		api("oauth-token", "OAuth token endpoint", "identity", "/oauth2/token/health", h),
		api("oidc-discovery", "OIDC discovery document", "identity", "/.well-known/openid-configuration", Spec{Interval: 300, JSON: map[string]any{"issuer": "https://id.northwind.example"}, Display: [][2]string{{"issuer", "Issuer"}}}),
		api("jwks", "JWKS endpoint", "identity", "/.well-known/jwks.json", Spec{Interval: 300, JSON: map[string]any{"keys_count": 2}}),
		api("mfa-api", "MFA / OTP service", "identity", "/api/mfa/health", h),
		{Key: "session-redis", Name: "Session store (Redis)", Type: "redis", Team: "identity", LatMin: 4, LatMax: 9,
			Host: "session-redis-01", Config: map[string]any{"host": e.fqdn("session-redis-01"), "port": 6379, "db": 1, "warn_latency_ms": 50, "max_latency_ms": 1000}},
		pg(e, "identity-db", "Identity DB (Postgres)", "identity", "identity-db-01", "SELECT 1", h),
		api("scim", "SCIM provisioning (B2B)", "identity", "/scim/v2/ServiceProviderConfig", Spec{Interval: 300}),
		{Key: "login-journey", Name: "Login flow (API journey)", Type: "synthetic_api", Team: "identity", Interval: 120, Timeout: 30, LatMin: 130, LatMax: 180,
			Config: map[string]any{"base_url": "http://" + e.fqdn("id") + fmt.Sprintf(":%d", e.port(offHTTP)), "steps": []any{
				map[string]any{"id": "discovery", "request": map[string]any{"method": "GET", "url": "/.well-known/openid-configuration"}, "assert": []any{map[string]any{"target": "json", "op": "exists", "path": "issuer"}}},
				map[string]any{"id": "token", "request": map[string]any{"method": "GET", "url": "/oauth2/token/health"}, "assert": []any{map[string]any{"target": "status", "op": "equals", "value": 200}}},
			}}},

		// ── data ──────────────────────────────────────────────────────
		pg(e, "orders-db", "Orders DB primary (Postgres)", "data", "db-orders-01", "SELECT count(*) FROM pg_stat_activity", Spec{Interval: 30, Tags: []string{"tier-1"}, NoOutages: true,
			Config: map[string]any{"query_value_op": "number_gt", "query_value": "0"}}),
		pg(e, "orders-db-replica", "Orders DB replica (Postgres)", "data", "db-orders-02", "SELECT 1", Spec{NoOutages: true}),
		pg(e, "analytics-db", "Analytics warehouse (Postgres)", "data", "warehouse-db-01", "SELECT 1", Spec{Notify: "custom-disabled", LatMin: 24, LatMax: 34}),
		{Key: "events-mq", Name: "Order events broker (RabbitMQ)", Type: "rabbitmq", Team: "data", LatMin: 9, LatMax: 13,
			Host: "rabbitmq-01", Config: map[string]any{"host": e.fqdn("rabbitmq-01"), "port": 5672, "username": "northwind", "password": "northwind-demo"}},
		tcp("kafka-1", "Kafka broker 1", "data", offTCP+4, Spec{Notify: "custom-disabled"}),
		tcp("kafka-2", "Kafka broker 2", "data", offTCP+5, Spec{Notify: "custom-disabled"}),
		tcp("kafka-3", "Kafka broker 3", "data", offTCP+6, Spec{Notify: "custom-disabled"}),
		push("etl-orders", "Nightly ETL — orders → warehouse", "data", 86400, 7200, Spec{Notify: "custom-disabled", PushPayload: map[string]string{"rows": "412880"}}),
		push("dbt-hourly", "dbt hourly build", "data", 3600, 900, Spec{Notify: "custom-empty", PushPayload: map[string]string{"models": "184"}}),
		{Key: "catalog-mongo", Name: "Catalog MongoDB (legacy)", Type: "mongodb", Team: "data", Profile: Paused, PausedAgo: 17 * 24 * time.Hour, LatMin: 3, LatMax: 8,
			Host: "mongo-catalog-01", Config: map[string]any{"host": e.fqdn("mongo-catalog-01"), "port": 27017}},
		{Key: "erp-mysql", Name: "Legacy ERP MySQL", Type: "mysql", Team: "data", Profile: Paused, PausedAgo: 24 * 24 * time.Hour, LatMin: 2, LatMax: 6,
			Host: "erp-db-01", Config: map[string]any{"host": e.fqdn("erp-db-01"), "port": 3306, "username": "erp_ro", "password": "erp-ro-demo"}},
		api("metabase", "Metabase BI", "data", "/bi/api/health", Spec{Notify: "custom-empty"}),
		api("export-api", "Data export API", "data", "/api/export/health", Spec{Notify: "custom-disabled"}),

		// ── platform ──────────────────────────────────────────────────
		api("api-gw", "API gateway", "platform", "/gateway/health", Spec{Interval: 30, Tags: []string{"tier-1"}, JSON: map[string]any{"version": "envoy 1.31.2", "upstreams_healthy": 42}, Display: [][2]string{{"upstreams_healthy", "Upstreams"}}}),
		api("api-gw-ready", "API gateway readiness", "platform", "/gateway/ready", h),
		api("ingress", "Ingress controller (eu-west-2)", "platform", "/healthz/ingress", h),
		tcp("nats", "NATS cluster", "platform", 0, Spec{Host: "nats", Config: map[string]any{"host": e.fqdn("nats"), "port": 4222}, LatMin: 0, LatMax: 1}),
		api("vault", "Vault", "platform", "/v1/sys/health", Spec{JSON: map[string]any{"sealed": false, "version": "1.17.6"}, Display: [][2]string{{"version", "Version"}}}),
		api("consul", "Consul", "platform", "/v1/status/leader", h),
		api("grafana", "Grafana", "platform", "/grafana/api/health", Spec{JSON: map[string]any{"database": "ok", "version": "11.2.0"}, Display: [][2]string{{"version", "Version"}}}),
		web("prometheus", "Prometheus server", "platform", "/prometheus/-/healthy", "Prometheus Server is Healthy", h),
		api("argocd", "Argo CD", "platform", "/argocd/healthz", h),
		api("registry", "Container registry", "platform", "/v2/", h),
		tcp("smtp-relay", "SMTP relay", "platform", offTCP+7, h),
		tcp("memcached", "Memcached", "platform", offTCP+8, h),
		// Real internet targets: latencies are what a London host sees
		// (anycast resolvers ~10-20 ms), so live checks continue the curve.
		{Key: "ping-cf", Name: "Ping — Cloudflare 1.1.1.1", Type: "ping", Team: "platform", Config: map[string]any{"host": "1.1.1.1"}, LatMin: 7, LatMax: 14},
		{Key: "ping-google", Name: "Ping — Google 8.8.8.8", Type: "ping", Team: "platform", Config: map[string]any{"host": "8.8.8.8"}, LatMin: 9, LatMax: 18},
		{Key: "ping-quad9", Name: "Ping — Quad9 9.9.9.9", Type: "ping", Team: "platform", Config: map[string]any{"host": "9.9.9.9"}, LatMin: 11, LatMax: 22},
		{Key: "ping-opendns", Name: "Ping — OpenDNS 208.67.222.222", Type: "ping", Team: "platform", Config: map[string]any{"host": "208.67.222.222"}, LatMin: 10, LatMax: 20},
		dns("dns-cloudflare", "DNS — cloudflare.com NS", "platform", "cloudflare.com", "NS", "1.1.1.1", h),
		dns("dns-google-mx", "DNS — google.com MX", "platform", "google.com", "MX", "9.9.9.9", h),
		{Key: "github-status", Name: "GitHub status (third-party)", Type: "http", Team: "platform", Profile: External, URL: "https://www.githubstatus.com/api/v2/status.json", Interval: 300, LatMin: 60, LatMax: 100,
			Display: [][2]string{{"status.description", "Status"}}},
		{Key: "cloudflare-status", Name: "Cloudflare status (third-party)", Type: "http", Team: "platform", Profile: External, URL: "https://www.cloudflarestatus.com/api/v2/status.json", Interval: 300, LatMin: 100, LatMax: 160,
			Display: [][2]string{{"status.description", "Status"}}},
		push("pg-backups", "Postgres backups (wal-g)", "platform", 3600, 900, Spec{PushPayload: map[string]string{"size_gb": "184"}}),
		push("cert-renewals", "cert-manager renewals", "platform", 21600, 3600, h),
		api("wiki", "Internal wiki", "platform", "/wiki/health", Spec{NoOutages: true, Tags: []string{"internal"}}),
		api("ci-runners", "CI runners", "platform", "/ci/health", Spec{NoOutages: true, Tags: []string{"internal"}, JSON: map[string]any{"idle_runners": 6}, Display: [][2]string{{"idle_runners", "Idle runners"}}}),
		tcp("artifact-cache", "Artifact cache", "platform", offTCP+9, Spec{NoOutages: true, Tags: []string{"internal"}}),
		{Key: "browser-checkout", Name: "Checkout journey (browser)", Type: "synthetic_browser", Team: "platform", Profile: Paused, PausedAgo: 15 * 24 * time.Hour, Interval: 300, Timeout: 60, LatMin: 2600, LatMax: 4200,
			Config: map[string]any{"start_url": e.url("checkout", "/checkout"), "steps": []any{map[string]any{"id": "title", "action": "assert_text", "selector": "h1", "value": "Secure checkout"}}}},

		// ── voice / contact centre ────────────────────────────────────
		sip("sip-trunk-a", "SIP trunk A — Colt (LON)", offSIP+0, Spec{Tags: []string{"tier-1", "carrier"}, LatMin: 14, LatMax: 32}),
		sip("sip-trunk-b", "SIP trunk B — BT (MAN)", offSIP+1, Spec{Tags: []string{"tier-1", "carrier"}, LatMin: 18, LatMax: 40}),
		sip("sbc-eu", "SBC — eu-west (OPTIONS)", offSIP+2, Spec{LatMin: 4, LatMax: 12}),
		sip("ivr", "IVR entry point", offSIP+3, Spec{LatMin: 9, LatMax: 22, DependsOn: []string{"sip-trunk-a", "sip-trunk-b", "sbc-eu"}}),
		api("cc-api", "Contact centre API", "voice", "/api/contact-centre/health", h),
		web("agent-desktop", "Agent desktop", "voice", "/agent-desktop", "Northwind Agent Desktop", Spec{DependsOn: []string{"cc-api"}}),
		wsS("chat-ws", "Live chat (WebSocket)", "voice", "/ws/chat", Spec{LatMin: 35, LatMax: 70, DependsOn: []string{"cc-api"}}),
		wsS("webrtc-ws", "WebRTC signalling (WebSocket)", "voice", "/ws/webrtc", Spec{LatMin: 50, LatMax: 95}),
		api("recordings", "Call recording API", "voice", "/api/recordings/health", slow),
		dns("dns-twilio", "DNS — api.twilio.com", "voice", "api.twilio.com", "CNAME", "1.1.1.1", h),
		{Key: "twilio-status", Name: "Twilio status (third-party)", Type: "http", Team: "voice", Profile: External, URL: "https://status.twilio.com/api/v2/status.json", Interval: 300, LatMin: 140, LatMax: 280,
			Display: [][2]string{{"status.description", "Status"}}},
		push("callback-sched", "Callback scheduler heartbeat", "voice", 60, 30, h),
		api("voicemail", "Voicemail transcription", "voice", "/api/voicemail/health", h),
		api("wfm", "Workforce management (WFM)", "voice", "/api/wfm/health", flaky),

		// ── multi-location "global" checks ────────────────────────────
		{Key: "geo-home", Interval: 120, Name: "Storefront home — global", Type: "http", Team: "storefront", Host: "www", Path: "/storefront/home", Keyword: "Free delivery over £50", Locations: glob, Quorum: 2, Tags: []string{"global"}},
		{Key: "geo-pdp", Interval: 120, Name: "Product page — global", Type: "http", Team: "storefront", Host: "www", Path: "/storefront/p/nw-48213", Keyword: "Add to basket", Locations: glob, Quorum: 2, Tags: []string{"global"}},
		{Key: "geo-track", Interval: 120, Name: "Tracking page — global", Type: "http", Team: "logistics", Host: "track", Path: "/track", Keyword: "Track your parcel", Locations: glob, Quorum: 2, Tags: []string{"global"}},
		{Key: "geo-login", Name: "Sign-in — global", Type: "http", Team: "identity", Path: "/account/login", Keyword: "Sign in to Northwind", Locations: glob, Quorum: 2, Tags: []string{"global"}},
		{Key: "geo-checkout", Name: "Checkout — global", Type: "http", Team: "checkout", Path: "/checkout", Keyword: "Secure checkout", Locations: glob, Quorum: 2, Tags: []string{"global"}, NoOutages: true},
		{Key: "geo-gw", Name: "API gateway — global", Type: "http", Team: "platform", Path: "/gateway/health", JSON: map[string]any{}, Locations: glob, Quorum: 2, Tags: []string{"global"}},
		{Key: "geo-payments", Name: "Payments API — global", Type: "http", Team: "payments", Path: "/api/payments/health", JSON: map[string]any{}, Locations: glob, Quorum: 2, Tags: []string{"global"}, NoOutages: true},
		{Key: "geo-search", Name: "Search API — global", Type: "http", Team: "search", Path: "/api/search/health", JSON: map[string]any{}, Locations: glob, Quorum: 2, Tags: []string{"global"}},
		{Key: "geo-cdn", Name: "CDN bundle — global", Type: "http", Team: "storefront", Path: "/static/app.4f9c2.js", Keyword: "webpackChunk", Locations: glob, Quorum: 2, Tags: []string{"global"}},
		{Key: "geo-status-api", Name: "Public status API — global", Type: "http", Team: "platform", Path: "/api/status/summary", JSON: map[string]any{"indicator": "minor"}, Locations: []string{"fra", "iad"}, Quorum: 1, Tags: []string{"global"}},
		{Key: "geo-sip", Name: "SIP trunk A — global reachability", Type: "sip", Team: "voice", Port: offSIP + 0, Locations: glob, Quorum: 2, Tags: []string{"global"}},

		// ── agents (OTLP push from `targets`) ─────────────────────────
		{Key: "host-web-01", Name: "Host — web-01.lon1", Type: "agent", Team: "storefront", Interval: 60},
		{Key: "host-web-02", Name: "Host — web-02.lon1", Type: "agent", Team: "storefront", Interval: 60},
		{Key: "host-db-orders", Name: "Host — db-orders-01.lon1", Type: "agent", Team: "data", Interval: 60,
			Config: map[string]any{"metric_rules": []any{
				map[string]any{"metric_name": "system.filesystem.utilization", "attribute_filters": map[string]any{"mountpoint": "/var/lib/postgresql"}, "operator": ">=", "threshold": 0.85},
				map[string]any{"metric_name": "system.cpu.utilization", "attribute_filters": map[string]any{"state": "used"}, "operator": ">=", "threshold": 0.9, "for_duration_seconds": 300},
			}}},
		{Key: "host-sbc-01", Name: "Host — sbc-01.lon1", Type: "agent", Team: "voice", Interval: 60,
			Config: map[string]any{"metric_rules": []any{
				map[string]any{"metric_name": "system.memory.utilization", "attribute_filters": map[string]any{"state": "used"}, "operator": ">=", "threshold": 0.92},
			}}},
	}

	groups := []Spec{
		group("g-storefront", "Storefront", "storefront", []string{"sf-home", "sf-category", "sf-pdp", "sf-search", "sf-basket", "sf-account", "product-api", "pricing-api", "sf-cdn"}, h),
		group("g-checkout", "Checkout flow", "checkout", []string{"checkout-web", "checkout-api", "cart-api", "cart-redis", "orders-api", "inventory-grpc", "checkout-journey"}, h),
		group("g-payments", "Payments", "payments", []string{"payments-api", "payment-auth", "payment-paypal", "payment-klarna", "payments-3ds", "fraud-grpc"}, h),
		group("g-search", "Search", "search", []string{"search-api", "search-suggest", "search-node-1", "search-node-2", "search-node-3", "search-indexer"}, h),
		group("g-carriers", "Carrier integrations", "logistics", []string{"carrier-dhl", "carrier-dpd", "carrier-ups", "carrier-royalmail", "carrier-evri", "tracking-api"}, h),
		group("g-warehouse", "Warehouse — Leeds DC", "logistics", []string{"wms-leeds", "label-svc", "label-printer", "stock-sync"}, h),
		group("g-identity", "Identity & SSO", "identity", []string{"login-page", "identity-api", "oauth-token", "oidc-discovery", "jwks", "mfa-api", "session-redis"}, h),
		group("g-data", "Data platform", "data", []string{"orders-db", "orders-db-replica", "analytics-db", "events-mq", "kafka-1", "kafka-2", "kafka-3"}, h),
		group("g-voice", "Voice & contact centre", "voice", []string{"sip-trunk-a", "sip-trunk-b", "sbc-eu", "ivr", "cc-api", "agent-desktop", "chat-ws"}, h),
		group("g-internal", "Internal tooling", "platform", []string{"wiki", "ci-runners", "artifact-cache"}, Spec{Rollup: "group"}),
		group("g-customer", "Customer-facing services", "platform", []string{"g-storefront", "g-checkout", "g-payments"}, h),
	}
	return append(specs, groups...)
}

// specDefaults fills derived fields so every consumer sees the same values.
func specDefaults(specs []Spec) {
	for i := range specs {
		s := &specs[i]
		if s.Interval == 0 {
			s.Interval = 60
		}
		if s.Threshold == 0 {
			s.Threshold = 2
		}
		if s.Profile == "" {
			s.Profile = Healthy
		}
		if s.Host == "" && s.Type != "group" && s.Type != "push" && s.Type != "agent" {
			s.Host = s.Key
			if h, ok := hostLabels[s.Key]; ok {
				s.Host = h
			}
		}
		// Push and agent monitors are dispatched too (the worker skips them),
		// and the scheduler gives every job a deadline of 2×timeout: a zero
		// timeout makes each job expire and write a "Job expired before
		// processing" platform error. Use the UI's default (10s, as the
		// MonitorForm sends for every type but the synthetics). Groups are
		// never dispatched and keep 0.
		if s.Timeout == 0 && s.Type != "group" {
			s.Timeout = min(10, s.Interval-1)
		}
		if s.LatMax == 0 {
			switch s.Profile {
			case Slow:
				s.LatMin, s.LatMax = 400, 1500
			default:
				switch s.Type {
				case "http", "synthetic_api":
					s.LatMin, s.LatMax = 25, 120
				case "tcp":
					s.LatMin, s.LatMax = 0, 1
				case "grpc":
					s.LatMin, s.LatMax = 3, 7
				case "websocket":
					s.LatMin, s.LatMax = 15, 50
				case "agent":
					// heartbeat latency = receipt delay of the export
					s.LatMin, s.LatMax = 300, 340
				default:
					s.LatMin, s.LatMax = 1, 10
				}
			}
		}
		if s.OutageRate == 0 && !s.NoOutages {
			s.OutageRate = 0.07
		}
		if s.Profile == Flaky && s.Threshold < 3 {
			s.Threshold = 3
		}
	}
}

func byKey(specs []Spec) map[string]*Spec {
	m := make(map[string]*Spec, len(specs))
	for i := range specs {
		m[specs[i].Key] = &specs[i]
	}
	return m
}

// sinEdge reports whether the spec checks a CDN-fronted host from
// Singapore, whose PoP serves slow 504s.
func (s *Spec) sinEdge() bool {
	if !sinEdgeHosts[s.Host] {
		return false
	}
	for _, l := range s.Locations {
		if l == "sin" {
			return true
		}
	}
	return false
}

// routeKey is what the mock serves one latency profile for: an HTTP/ws
// path or a SIP responder port ("" = per-spec).
func (s *Spec) routeKey() string {
	switch {
	case s.Path != "":
		return s.Path
	case s.Type == "sip":
		return fmt.Sprintf("sip:%d", s.Port)
	}
	return ""
}

// monitorURL is the URL an http spec checks.
func (s *Spec) monitorURL(e Env) string {
	if s.URL != "" {
		return s.URL
	}
	return e.url(s.Host, s.Path)
}

// hostLabels names each service's host (under Domain). Specs absent here
// use their key. Several monitors share a host where the real estate would
// (the storefront pages all live on www, the PSP checks on payments-api).
var hostLabels = map[string]string{
	"sf-home": "www", "sf-category": "www", "sf-pdp": "www", "sf-search": "www", "sf-basket": "www", "sf-account": "www",
	"sf-stores": "www", "sf-sitemap": "www", "sf-robots": "www", "order-history": "www", "sf-journal": "journal",
	"sf-cdn": "cdn", "sf-images": "images", "product-api": "catalog-api", "cms-api": "cms-proxy", "giftcards": "giftcards-api",
	"sf-bf-queue": "queue", "sf-v1": "legacy-www",
	"checkout-web": "checkout", "promo-api": "promotions-api", "tax-api": "tax-proxy", "address-api": "address-proxy", "basket-ws": "realtime",
	"payment-auth": "payments-api", "payment-paypal": "payments-api", "payment-klarna": "payments-api", "payment-capture": "payments-api",
	"payment-refunds": "payments-api", "payments-3ds": "payments-api", "payments-webhooks": "webhooks",
	"search-suggest": "search-api", "search-indexer": "search-indexer", "search-synonyms": "synonyms", "search-ranking": "ranking",
	"search-qu": "query-understanding", "search-api": "search-api",
	"track-page": "track", "carrier-dhl": "carrier-gateway", "carrier-dpd": "carrier-gateway", "carrier-ups": "carrier-gateway",
	"carrier-royalmail": "carrier-gateway", "carrier-evri": "carrier-gateway", "returns": "returns", "label-svc": "labels",
	"route-grpc": "route-optimiser", "dispatch-ws": "dispatch", "sftp-meridian": "sftp.meridian-wholesale", "edi-as2": "as2",
	"login-page": "account", "identity-api": "id", "oauth-token": "id", "oidc-discovery": "id", "jwks": "id", "mfa-api": "mfa",
	"metabase": "bi", "kafka-1": "kafka-1", "export-api": "export-api",
	"api-gw": "gateway", "api-gw-ready": "gateway", "smtp-relay": "smtp", "memcached": "memcached-01", "ci-runners": "ci",
	"browser-checkout": "checkout", "sip-trunk-a": "sip-colt", "sip-trunk-b": "sip-bt", "sbc-eu": "sbc-eu", "ivr": "ivr", "cc-api": "contact-centre-api",
	"agent-desktop": "agent", "chat-ws": "chat", "webrtc-ws": "webrtc", "recordings": "recordings",
	"geo-login": "account", "geo-checkout": "checkout", "geo-gw": "gateway", "geo-payments": "payments-api", "geo-search": "search-api",
	"geo-cdn": "cdn", "geo-status-api": "status-api", "geo-sip": "sip-colt",
}
