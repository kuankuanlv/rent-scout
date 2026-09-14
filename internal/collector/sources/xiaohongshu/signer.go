package xiaohongshu

import (
	"context"
)

type SignRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   string `json:"body"`
}

type SignResult struct {
	XS        string `json:"x-s"`
	XT        string `json:"x-t"`
	XSCommon  string `json:"x-s-common"`
	XRapParam string `json:"x-rap-param,omitempty"`
}

type Signer interface {
	Sign(context.Context, SignRequest) (SignResult, error)
}

type LocalSigner struct{}

func (s *LocalSigner) Sign(ctx context.Context, req SignRequest) (SignResult, error) {
	return signInternal(req.Path, []byte(req.Body), nil)
}

func SignHeaders(uri string, payload []byte, cookies map[string]string) (map[string]string, error) {
	res, err := signInternal(uri, payload, cookies)
	if err != nil {
		return nil, err
	}
	
	return map[string]string{
		"x-s":         res.XS,
		"x-t":         res.XT,
		"x-s-common":  res.XSCommon,
		"x-rap-param": res.XRapParam,
	}, nil
}
