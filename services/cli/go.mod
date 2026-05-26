module github.com/Harporis/harporis/services/cli

go 1.26

require (
	github.com/Harporis/harporis/contracts v0.0.0-00010101000000-000000000000
	github.com/Harporis/harporis/kit v0.0.0
	github.com/charmbracelet/lipgloss v1.1.0
	github.com/google/uuid v1.6.0
	github.com/muesli/termenv v0.16.0
	github.com/nats-io/nats-server/v2 v2.14.1
	github.com/spf13/cobra v1.10.2
	google.golang.org/protobuf v1.36.11
)

replace (
	github.com/Harporis/harporis/contracts => ../../contracts
	github.com/Harporis/harporis/kit => ../../kit
)

require (
	github.com/antithesishq/antithesis-sdk-go v0.7.0-default-no-op // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/charmbracelet/colorprofile v0.2.3-0.20250311203215-f60798e515dc // indirect
	github.com/charmbracelet/x/ansi v0.8.0 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.13-0.20250311204145-2c3ea96c31dd // indirect
	github.com/charmbracelet/x/term v0.2.1 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/minio/highwayhash v1.0.4 // indirect
	github.com/nats-io/jwt/v2 v2.8.1 // indirect
	github.com/nats-io/nats.go v1.52.0 // indirect
	github.com/nats-io/nkeys v0.4.15 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/crypto v0.51.0 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/grpc v1.81.1 // indirect
)
