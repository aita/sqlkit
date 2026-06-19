// This is a separate module so the OpenTelemetry SDK dependency tree does not
// reach the root sqlkit module (which now includes the querytrace package) that
// library consumers import.
module github.com/aita/sqlkit/querytrace/otelquery

go 1.25.0

require (
	github.com/aita/sqlkit v0.0.0
	go.opentelemetry.io/otel v1.44.0
	go.opentelemetry.io/otel/metric v1.44.0
	go.opentelemetry.io/otel/sdk v1.44.0
	go.opentelemetry.io/otel/sdk/metric v1.44.0
	go.opentelemetry.io/otel/trace v1.44.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	golang.org/x/sys v0.45.0 // indirect
)

replace github.com/aita/sqlkit => ../..
