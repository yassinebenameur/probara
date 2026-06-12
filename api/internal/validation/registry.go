package validation

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// ConfigValidator defines the interface for monitor config validation
type ConfigValidator interface {
	ValidateConfig(config json.RawMessage) error
}

// ValidatorRegistry manages registered validators for different monitor types
type ValidatorRegistry struct {
	validators map[models.MonitorType]ConfigValidator
	mu         sync.RWMutex
}

// NewValidatorRegistry creates a new validator registry
func NewValidatorRegistry() *ValidatorRegistry {
	return &ValidatorRegistry{
		validators: make(map[models.MonitorType]ConfigValidator),
	}
}

// Register adds a validator for a specific monitor type
func (r *ValidatorRegistry) Register(monitorType models.MonitorType, validator ConfigValidator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.validators[monitorType] = validator
}

// Validate validates a config for a specific monitor type
func (r *ValidatorRegistry) Validate(monitorType models.MonitorType, config json.RawMessage) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	validator, ok := r.validators[monitorType]
	if !ok {
		return fmt.Errorf("unknown monitor type: %s", monitorType)
	}
	return validator.ValidateConfig(config)
}

// Has checks if a validator is registered for a monitor type
func (r *ValidatorRegistry) Has(monitorType models.MonitorType) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.validators[monitorType]
	return ok
}

// Types returns all registered monitor types
func (r *ValidatorRegistry) Types() []models.MonitorType {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]models.MonitorType, 0, len(r.validators))
	for t := range r.validators {
		types = append(types, t)
	}
	return types
}

// DefaultRegistry is the global validator registry with default validators
var DefaultRegistry *ValidatorRegistry

func init() {
	DefaultRegistry = NewDefaultValidatorRegistry()
}

// NewDefaultValidatorRegistry creates a registry with default validators
func NewDefaultValidatorRegistry() *ValidatorRegistry {
	registry := NewValidatorRegistry()

	// Register default validators
	registry.Register(models.MonitorTypeHTTP, &HTTPConfigValidator{})
	registry.Register(models.MonitorTypePing, &PingConfigValidator{})
	registry.Register(models.MonitorTypeDNS, &DNSConfigValidator{})
	registry.Register(models.MonitorTypeGRPC, &GRPCConfigValidator{})
	registry.Register(models.MonitorTypeGroup, &GroupConfigValidator{})
	registry.Register(models.MonitorTypeAgent, &AgentConfigValidator{})
	registry.Register(models.MonitorTypePush, &PushConfigValidator{})
	registry.Register(models.MonitorTypeSIP, &SIPConfigValidator{})
	registry.Register(models.MonitorTypeSyntheticAPI, &SyntheticAPIConfigValidator{})
	registry.Register(models.MonitorTypeSyntheticBrowser, &SyntheticBrowserConfigValidator{})
	registry.Register(models.MonitorTypeRedis, &RedisConfigValidator{})
	registry.Register(models.MonitorTypePostgres, &PostgresConfigValidator{})
	registry.Register(models.MonitorTypeMongoDB, &MongoDBConfigValidator{})
	registry.Register(models.MonitorTypeRabbitMQ, &RabbitMQConfigValidator{})

	return registry
}
