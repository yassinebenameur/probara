package validation

import (
	"reflect"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

// ConfigTyped is implemented by every registered validator: it names the Go
// struct its ValidateConfig decodes `config` into. The MCP server reflects
// that struct into the JSON schema it hands agents (describe_monitor_type),
// so the schema an agent sees is the shape the validator actually reads.
// TestEveryValidatorNamesItsConfigType keeps new types from skipping it.
type ConfigTyped interface {
	ConfigType() reflect.Type
}

// ConfigType returns the config struct of a registered monitor type.
func (r *ValidatorRegistry) ConfigType(monitorType models.MonitorType) (reflect.Type, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	typed, ok := r.validators[monitorType].(ConfigTyped)
	if !ok {
		return nil, false
	}
	return typed.ConfigType(), true
}

func (*PrometheusConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.PrometheusMonitorConfig]()
}
func (*HTTPConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.HTTPMonitorConfig]()
}
func (*PingConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.PingMonitorConfig]()
}
func (*DNSConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.DNSMonitorConfig]()
}
func (*GRPCConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.GRPCMonitorConfig]()
}
func (*TCPConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.TCPMonitorConfig]()
}
func (*WebSocketConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.WebSocketMonitorConfig]()
}
func (*GroupConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[models.GroupConfig]()
}
func (*AgentConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[models.AgentConfig]()
}
func (*PushConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[models.PushConfig]()
}
func (*SIPConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.SIPMonitorConfig]()
}
func (*SyntheticAPIConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.SyntheticAPIMonitorConfig]()
}
func (*SyntheticBrowserConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.SyntheticBrowserMonitorConfig]()
}
func (*RedisConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.RedisMonitorConfig]()
}
func (*PostgresConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.PostgresMonitorConfig]()
}
func (*MongoDBConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.MongoDBMonitorConfig]()
}
func (*MySQLConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.MySQLMonitorConfig]()
}
func (*RabbitMQConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.RabbitMQMonitorConfig]()
}
