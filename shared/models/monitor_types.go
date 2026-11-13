package models

// MonitorType is the kind of a monitor. The constants below are the single
// list of monitor types in Go: the API validator registry, the worker's
// checker registry and the status-page display mirrors all name types through
// them, and parity tests in each of those packages fail when a type is added
// here without being handled there.
type MonitorType string

const (
	MonitorTypePrometheus       MonitorType = "prometheus"
	MonitorTypeHTTP             MonitorType = "http"
	MonitorTypePing             MonitorType = "ping"
	MonitorTypeDNS              MonitorType = "dns"
	MonitorTypeGRPC             MonitorType = "grpc"
	MonitorTypeGroup            MonitorType = "group"
	MonitorTypeAgent            MonitorType = "agent"
	MonitorTypePush             MonitorType = "push"
	MonitorTypeSIP              MonitorType = "sip"
	MonitorTypeSyntheticAPI     MonitorType = "synthetic_api"
	MonitorTypeSyntheticBrowser MonitorType = "synthetic_browser"
	MonitorTypeRedis            MonitorType = "redis"
	MonitorTypePostgres         MonitorType = "postgres"
	MonitorTypeMongoDB          MonitorType = "mongodb"
	MonitorTypeRabbitMQ         MonitorType = "rabbitmq"
	MonitorTypeTCP              MonitorType = "tcp"
	MonitorTypeMySQL            MonitorType = "mysql"
	MonitorTypeWebSocket        MonitorType = "websocket"
)

// AllMonitorTypes lists every monitor type a user can create. It does not
// include MonitorTypeMeshProbe, which is a check-job type, not a monitor.
var AllMonitorTypes = []MonitorType{
	MonitorTypeHTTP,
	MonitorTypePing,
	MonitorTypeDNS,
	MonitorTypeGRPC,
	MonitorTypeGroup,
	MonitorTypeAgent,
	MonitorTypePush,
	MonitorTypeSIP,
	MonitorTypeSyntheticAPI,
	MonitorTypeSyntheticBrowser,
	MonitorTypeRedis,
	MonitorTypePostgres,
	MonitorTypeMongoDB,
	MonitorTypeRabbitMQ,
	MonitorTypeTCP,
	MonitorTypeMySQL,
	MonitorTypeWebSocket,
	MonitorTypePrometheus,
}
