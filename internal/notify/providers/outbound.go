package providers

import (
	"errors"
	"net/url"
)

// stripURL drops the request URL from an *url.Error so a destination URL
// (which often carries a token in its path or query) does not end up in
// logs or dead letters. The wrapped cause is kept, so errors.Is checks for
// checker.ErrSSRFBlocked and timeouts still work.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}
