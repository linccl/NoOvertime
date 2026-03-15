module noovertime

go 1.22

require github.com/jackc/pgx/v5 v5.5.5

require (
	github.com/aliyun/aliyun-oss-go-sdk v1.9.8
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jackc/puddle/v2 v2.2.1 // indirect
	golang.org/x/crypto v0.17.0 // indirect
	golang.org/x/sync v0.1.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	golang.org/x/time v0.0.0-00010101000000-000000000000 // indirect
)

replace golang.org/x/time => github.com/golang/time v0.5.0
