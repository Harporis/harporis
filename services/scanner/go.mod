module github.com/Harporis/harporis/services/scanner

go 1.26

replace (
	github.com/Harporis/harporis/contracts => ../../contracts
	github.com/Harporis/harporis/kit => ../../kit
)

require github.com/Harporis/harporis/contracts v0.0.0-00010101000000-000000000000

require (
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/grpc v1.81.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
