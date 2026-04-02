package models

import "errors"

var (
	ErrInvalidAccountAccessOption  = errors.New("invalid account access option")
	ErrUserAlreadyExists           = errors.New("user already exists")
	ErrUserNotExists               = errors.New("user not exists")
	ErrInvalidOperation            = errors.New("invalid operation")
	ErrUserDoNotHaveGithubAccess   = errors.New("user do not have github access")
	ErrInvalidEmailAddr            = errors.New("invalid email address")
	ErrWrongVerificationCode       = errors.New("wrong verification code")
	ErrDeployPipelineTimeout       = errors.New("deploy pipeline timed out")
	ErrUnexpected                  = errors.New("unexpected error occured")
	ErrOrchestratorFailed          = errors.New("orchestrator returned failure")
	ErrConfirmationTimeout         = errors.New("confirmation timeout")
	ErrInvalidRequest              = errors.New("invalid request")
)
