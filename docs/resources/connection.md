# deixic_connection

Enrolls a connector through the connectors service managed exchange (`POST /v1/managed-provider-exchanges`). Set the provider's `connectors_endpoint` (or `DEIXIC_CONNECTORS_ENDPOINT`) when the connectors service is not behind `endpoint`.

Credentials are references only. `credential_refs` takes references to secrets held elsewhere, and the references the exchange issues are stored as `issued_credential_refs`. No secret value is accepted or stored. `actor_id` must be the subject of the bearer token.

The connectors service offers enrollment here but no read or delete, so every attribute forces replacement and destroy removes the resource from state only. Enrollment is idempotent on `connection_id`.
