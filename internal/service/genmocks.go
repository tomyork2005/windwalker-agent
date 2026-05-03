//go:build generate

package service

//go:generate go run github.com/gojuno/minimock/v3/cmd/minimock -g -i agent/internal/service.XrayDriver -o ./mocks/xray_driver_mock.go -n XrayDriverMock
//go:generate go run github.com/gojuno/minimock/v3/cmd/minimock -g -i agent/internal/service.TaskRepo   -o ./mocks/task_repo_mock.go   -n TaskRepoMock
//go:generate go run github.com/gojuno/minimock/v3/cmd/minimock -g -i agent/internal/service.Sender     -o ./mocks/sender_mock.go     -n SenderMock
