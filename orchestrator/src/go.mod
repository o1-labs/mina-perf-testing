module itn_orchestrator

go 1.23

toolchain go1.24.4

require (
	github.com/Khan/genqlient v0.5.0
	github.com/aws/aws-sdk-go-v2 v1.21.1
	github.com/aws/aws-sdk-go-v2/config v1.18.44
	github.com/aws/aws-sdk-go-v2/service/s3 v1.40.1
	github.com/btcsuite/btcutil v1.0.2
	github.com/ipfs/go-log/v2 v2.1.3
	github.com/stretchr/testify v1.8.1
	go.uber.org/zap v1.16.0
	golang.org/x/crypto v0.22.0
	gorm.io/gorm v1.25.11
	itn_json_types v0.0.0
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/go-sql-driver/mysql v1.8.1 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20231201235250-de7065d80cb9 // indirect
	github.com/jackc/pgx/v5 v5.5.5 // indirect
	github.com/jackc/puddle/v2 v2.2.1 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	gorm.io/driver/mysql v1.5.6 // indirect
)

require (
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.4.14 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.13.42 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.13.12 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.1.42 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.4.36 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.3.44 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.1.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.9.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.1.37 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.9.36 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.15.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.15.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.17.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.23.1 // indirect
	github.com/aws/smithy-go v1.15.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/gorilla/mux v1.8.1
	github.com/lib/pq v1.10.9
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/vektah/gqlparser/v2 v2.4.5 // indirect
	go.uber.org/atomic v1.7.0 // indirect
	go.uber.org/multierr v1.6.0 // indirect
	golang.org/x/lint v0.0.0-20210508222113-6edffad5e616 // indirect
	golang.org/x/sys v0.26.0 // indirect
	golang.org/x/tools v0.26.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	gorm.io/datatypes v1.2.5
	gorm.io/driver/postgres v1.5.11
	honnef.co/go/tools v0.0.1-2020.1.4 // indirect
)

replace itn_json_types => ../json_types
