module github.com/pockethost/pocketone/ext/docker

go 1.27

require (
	github.com/Masterminds/semver/v3 v3.5.0
	github.com/pockethost/pocketone/ext/host v0.0.0-00010101000000-000000000000
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/pockethost/pocketone/ext/host => ../host
