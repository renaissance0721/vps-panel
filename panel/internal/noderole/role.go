package noderole

import "errors"

const (
	Direct  = "direct"
	Landing = "landing"
)

var ErrInvalid = errors.New("node role must be direct or landing")

func Validate(value string) error {
	if value != Direct && value != Landing {
		return ErrInvalid
	}
	return nil
}
