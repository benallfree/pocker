module github.com/pockethost/pocketbase/ext/firewall

go 1.27

require (
	github.com/pockethost/pocketbase/ext/host v0.0.0-00010101000000-000000000000
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/pockethost/pocketbase/ext/host => ../host
