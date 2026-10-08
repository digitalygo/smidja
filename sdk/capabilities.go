package sdk

import "errors"

var ErrUnsupported = errors.New("sdk: the active transport or model does not support this operation")
