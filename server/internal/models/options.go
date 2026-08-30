package models

import "time"

// OptionValueType enumerates the wire encodings a DHCP option's value can
// take. 'fqdn' and 'route_list' are amendment A5.4 additions to the base
// schema.sql set (route_list backs option 121, classless static routes).
type OptionValueType string

const (
	OptionValueTypeString    OptionValueType = "string"
	OptionValueTypeUint8     OptionValueType = "uint8"
	OptionValueTypeUint16    OptionValueType = "uint16"
	OptionValueTypeUint32    OptionValueType = "uint32"
	OptionValueTypeIP        OptionValueType = "ip"
	OptionValueTypeIPList    OptionValueType = "ip_list"
	OptionValueTypeBytes     OptionValueType = "bytes"
	OptionValueTypeBoolean   OptionValueType = "boolean"
	OptionValueTypeFQDN      OptionValueType = "fqdn"
	OptionValueTypeRouteList OptionValueType = "route_list"
)

// Valid reports whether t is a known option value type.
func (t OptionValueType) Valid() bool {
	switch t {
	case OptionValueTypeString, OptionValueTypeUint8, OptionValueTypeUint16,
		OptionValueTypeUint32, OptionValueTypeIP, OptionValueTypeIPList,
		OptionValueTypeBytes, OptionValueTypeBoolean, OptionValueTypeFQDN,
		OptionValueTypeRouteList:
		return true
	default:
		return false
	}
}

// OptionScopeLevel enumerates the levels of the option inheritance
// hierarchy. Precedence when resolving an effective value (highest first):
// reservation > pool > scope > template > global.
type OptionScopeLevel string

const (
	OptionScopeGlobal      OptionScopeLevel = "global"
	OptionScopeTemplate    OptionScopeLevel = "template"
	OptionScopeScope       OptionScopeLevel = "scope"
	OptionScopePool        OptionScopeLevel = "pool"
	OptionScopeReservation OptionScopeLevel = "reservation"
)

// Valid reports whether l is a known scope level.
func (l OptionScopeLevel) Valid() bool {
	switch l {
	case OptionScopeGlobal, OptionScopeTemplate, OptionScopeScope, OptionScopePool, OptionScopeReservation:
		return true
	default:
		return false
	}
}

// OptionCatalogEntry describes one IANA DHCPv4 option code
// (dhcp_option_catalog table): its name, wire type, and documentation.
// Every option surfaced anywhere in the UI/API carries this description.
type OptionCatalogEntry struct {
	OptionCode    int16           `json:"option_code" db:"option_code"`
	Name          string          `json:"name" db:"name"`
	ValueType     OptionValueType `json:"value_type" db:"value_type"`
	Description   string          `json:"description" db:"description"`
	IANAReference *string         `json:"iana_reference" db:"iana_reference"`
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
}

// DHCPOption represents one configured option value at a given scope level
// (dhcp_option_values table). It is also referred to as "OptionValue" in
// docs/api/endpoints.md; the two names describe the same entity/table.
type DHCPOption struct {
	ID              string           `json:"id" db:"id"`
	OptionCode      int16            `json:"option_code" db:"option_code"`
	ScopeLevel      OptionScopeLevel `json:"scope_level" db:"scope_level"`
	ScopeEntityID   string           `json:"scope_entity_id" db:"scope_entity_id"`
	Value           string           `json:"value" db:"value"`
	IsEnabled       bool             `json:"is_enabled" db:"is_enabled"`
	CreatedAt       time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at" db:"updated_at"`
	CreatedByUserID *int64           `json:"created_by_user_id,omitempty" db:"created_by_user_id"`
	UpdatedByUserID *int64           `json:"updated_by_user_id,omitempty" db:"updated_by_user_id"`

	// Catalog is populated by handlers/store lookups that join the catalog
	// entry so the API always surfaces the option's human-readable
	// description alongside its raw value (quality bar requirement).
	Catalog *OptionCatalogEntry `json:"catalog,omitempty" db:"-"`
}

// OptionValue is an alias for DHCPOption: docs/api/endpoints.md names the
// scope/pool/template option-value response shape "OptionValue" while
// CONTRACTS.md Part 1 names the same table's Go struct "DHCPOption".
type OptionValue = DHCPOption
