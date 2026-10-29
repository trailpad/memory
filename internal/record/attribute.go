package record

import "encoding/json"

// Attributes are values the spec does not name, keyed by namespaced type
// ("crm.amount"). They are kept as written; a profile gives them a schema.
type Attributes map[Type]json.RawMessage
