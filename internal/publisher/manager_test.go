package publisher

import (
	"strings"
	"testing"
)

// The payloads below mirror the NATS server's ServerAPIClaimUpdateResponse, which
// reports rejected claims in the Error field of an otherwise successful reply.
func TestCheckClaimsResponse(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		wantStatus string
		wantErr    string
	}{
		{
			name:       "jwt updated",
			payload:    `{"server":{"name":"n1"},"data":{"account":"ABC","code":200,"message":"jwt updated"}}`,
			wantStatus: "jwt updated",
		},
		{
			// The server answers this way when it already holds an equal-or-newer
			// JWT, which is what makes reconciliation cheap. It is a success.
			name:       "jwt update skipped",
			payload:    `{"server":{"name":"n1"},"data":{"account":"ABC","code":200,"message":"jwt update skipped"}}`,
			wantStatus: "jwt update skipped",
		},
		{
			name:       "deleted accounts",
			payload:    `{"server":{"name":"n1"},"data":{"code":200,"message":"deleted 1 accounts"}}`,
			wantStatus: "deleted 1 accounts",
		},
		{
			name:    "validation failure",
			payload: `{"server":{"name":"n1"},"error":{"account":"ABC","code":500,"description":"jwt validation failed - invalid signature"}}`,
			wantErr: "jwt validation failed",
		},
		{
			name:    "partial delete failure",
			payload: `{"server":{"name":"n1"},"error":{"code":500,"description":"deleted 2 accounts, failed for 1"}}`,
			wantErr: "failed for 1",
		},
		{
			name:    "error without description",
			payload: `{"server":{"name":"n1"},"error":{"code":500}}`,
			wantErr: "unspecified error",
		},
		{
			// An unparseable reply is not proof of failure, so it is reported as-is
			// rather than sending an account into the retry loop.
			name:       "unrecognized payload",
			payload:    `not json at all`,
			wantStatus: "not json at all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, err := checkClaimsResponse([]byte(tt.payload))

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("checkClaimsResponse() error = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("checkClaimsResponse() error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("checkClaimsResponse() unexpected error: %v", err)
			}
			if status != tt.wantStatus {
				t.Errorf("checkClaimsResponse() status = %q, want %q", status, tt.wantStatus)
			}
		})
	}
}
