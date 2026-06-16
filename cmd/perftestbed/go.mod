module go.opentelemetry.io/collector/cmd/perftestbed

go 1.25.0

require (
	go.opentelemetry.io/collector/consumer v1.58.0
	go.opentelemetry.io/collector/consumer/consumertest v0.152.0
	go.opentelemetry.io/collector/internal/fanoutconsumer v0.152.0
	go.opentelemetry.io/collector/pdata v1.58.0
	go.opentelemetry.io/collector/pdata/testdata v0.152.0
)

require (
	github.com/hashicorp/go-version v1.9.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	go.opentelemetry.io/collector/consumer/xconsumer v0.152.0 // indirect
	go.opentelemetry.io/collector/featuregate v1.58.0 // indirect
	go.opentelemetry.io/collector/pdata/pprofile v0.152.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
)

replace go.opentelemetry.io/collector/consumer => ../../consumer

replace go.opentelemetry.io/collector/consumer/consumertest => ../../consumer/consumertest

replace go.opentelemetry.io/collector/consumer/xconsumer => ../../consumer/xconsumer

replace go.opentelemetry.io/collector/featuregate => ../../featuregate

replace go.opentelemetry.io/collector/internal/fanoutconsumer => ../../internal/fanoutconsumer

replace go.opentelemetry.io/collector/pdata => ../../pdata

replace go.opentelemetry.io/collector/pdata/pprofile => ../../pdata/pprofile

replace go.opentelemetry.io/collector/pdata/testdata => ../../pdata/testdata
