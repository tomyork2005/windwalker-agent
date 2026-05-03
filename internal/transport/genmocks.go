//go:build generate

package transport

//go:generate go run github.com/gojuno/minimock/v3/cmd/minimock -g -i agent/internal/transport.Service -o ./mocks/service_mock.go -n ServiceMock
