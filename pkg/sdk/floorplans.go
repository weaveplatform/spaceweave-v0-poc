package sdk

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"iter"
	"mime/multipart"
	"net/http"
	"path"
	"strings"

	"github.com/weaveplatform/spaceweave/pkg/client/api"
	"github.com/weaveplatform/spaceweave/pkg/client/operator"
)

// ReviewAction identifies one explicit lifecycle operation.
type ReviewAction string

const (
	Approve    ReviewAction = "approve"
	Reject     ReviewAction = "reject"
	RetryTrace ReviewAction = "retry"
	Retrace    ReviewAction = "retrace"
)

func (c *Client) ListFloorplans(
	ctx context.Context,
	params *api.ListFloorplansParams,
) (*api.FloorplanPage, error) {
	return decoded[api.FloorplanPage](c.API.ListFloorplans(ctx, params))
}

// Floorplans walks pages lazily, stopping when the caller breaks iteration.
// Repeated cursors fail instead of creating an endless request loop.
func (c *Client) Floorplans(
	ctx context.Context,
	params *api.ListFloorplansParams,
) iter.Seq2[api.Floorplan, error] {
	return func(yield func(api.Floorplan, error) bool) {
		var query api.ListFloorplansParams
		if params != nil {
			query = *params
		}
		seen := map[string]bool{}
		if query.Cursor != nil {
			seen[*query.Cursor] = true
		}
		for {
			page, err := c.ListFloorplans(ctx, &query)
			if err != nil {
				yield(api.Floorplan{}, err)
				return
			}
			for _, plan := range page.Plans {
				if !yield(plan, nil) {
					return
				}
			}
			if page.NextCursor == nil || *page.NextCursor == "" {
				return
			}
			cursor := *page.NextCursor
			if seen[cursor] {
				yield(api.Floorplan{}, ErrPagination)
				return
			}
			seen[cursor] = true
			query.Cursor = &cursor
		}
	}
}

func (c *Client) GetFloorplan(ctx context.Context, id string) (*api.Floorplan, error) {
	return decoded[api.Floorplan](c.API.GetFloorplan(ctx, id))
}

// multipartBody streams file bytes without buffering the drawing or starting
// a goroutine that could remain blocked if a server refuses the upload early.
func multipartBody(field, name, role string, source io.Reader) (string, io.Reader, error) {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	if source == nil || name == "." || name == "/" || name == "" ||
		strings.ContainsAny(name, "\r\n") {
		return "", nil, fmt.Errorf("%w: supply a file name and reader", ErrInput)
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if role != "" {
		if err := writer.WriteField("role", role); err != nil {
			return "", nil, fmt.Errorf("multipart role: %w", err)
		}
	}
	if _, err := writer.CreateFormFile(field, name); err != nil {
		return "", nil, fmt.Errorf("multipart file: %w", err)
	}
	header := bytes.Clone(buffer.Bytes())
	buffer.Reset()
	if err := writer.Close(); err != nil {
		return "", nil, fmt.Errorf("multipart boundary: %w", err)
	}
	return writer.FormDataContentType(), io.MultiReader(
		bytes.NewReader(header),
		source,
		bytes.NewReader(buffer.Bytes()),
	), nil
}

func (c *Client) UploadFloorplan(
	ctx context.Context,
	name string,
	source io.Reader,
) (*api.UploadReply, error) {
	contentType, body, err := multipartBody("image", name, "", source)
	if err != nil {
		return nil, err
	}
	return decoded[api.UploadReply](
		c.API.UploadFloorplanWithBody(
			ctx,
			&api.UploadFloorplanParams{XSpaceweave: api.UploadFloorplanParamsXSpaceweaveN1},
			contentType,
			body,
		),
	)
}

func (c *Client) ReviewFloorplan(
	ctx context.Context,
	id string,
	action ReviewAction,
	note string,
) (*api.ReviewReply, error) {
	body := api.ReviewRequest{Note: &note}
	switch action {
	case Approve:
		return decoded[api.ReviewReply](
			c.API.ApproveFloorplan(
				ctx,
				id,
				&api.ApproveFloorplanParams{XSpaceweave: api.ApproveFloorplanParamsXSpaceweaveN1},
				body,
			),
		)
	case Reject:
		return decoded[api.ReviewReply](
			c.API.RejectFloorplan(
				ctx,
				id,
				&api.RejectFloorplanParams{XSpaceweave: api.RejectFloorplanParamsXSpaceweaveN1},
				body,
			),
		)
	case RetryTrace:
		return decoded[api.ReviewReply](
			c.API.RetryFloorplan(
				ctx,
				id,
				&api.RetryFloorplanParams{XSpaceweave: api.RetryFloorplanParamsXSpaceweaveN1},
				body,
			),
		)
	case Retrace:
		return decoded[api.ReviewReply](
			c.API.RetraceFloorplan(
				ctx,
				id,
				&api.RetraceFloorplanParams{XSpaceweave: api.RetraceFloorplanParamsXSpaceweaveN1},
				body,
			),
		)
	default:
		return nil, fmt.Errorf("%w: unknown review action %q", ErrInput, action)
	}
}

func (c *Client) AttachFile(
	ctx context.Context,
	id, name string,
	role api.AttachFloorplanFileMultipartBodyRole,
	source io.Reader,
) (*api.UploadReply, error) {
	if !role.Valid() {
		return nil, fmt.Errorf("%w: attachment role %q", ErrInput, role)
	}
	contentType, body, err := multipartBody("file", name, string(role), source)
	if err != nil {
		return nil, err
	}
	return decoded[api.UploadReply](
		c.API.AttachFloorplanFileWithBody(
			ctx,
			id,
			&api.AttachFloorplanFileParams{XSpaceweave: api.AttachFloorplanFileParamsXSpaceweaveN1},
			contentType,
			body,
		),
	)
}

func (c *Client) DetachFile(ctx context.Context, id, sha string) (*api.UploadReply, error) {
	return decoded[api.UploadReply](
		c.API.DetachFloorplanFile(
			ctx,
			id,
			sha,
			&api.DetachFloorplanFileParams{XSpaceweave: api.DetachFloorplanFileParamsXSpaceweaveN1},
		),
	)
}

func (c *Client) GetProcessing(ctx context.Context, id string) (*api.Processing, error) {
	return decoded[api.Processing](c.API.GetFloorplanProcessing(ctx, id))
}

func (c *Client) GetProfile(ctx context.Context, name string) (*api.ProfileReply, error) {
	return decoded[api.ProfileReply](c.API.GetLayerProfile(ctx, name))
}

func (c *Client) PutProfile(
	ctx context.Context,
	name string,
	profile api.LayerProfile,
) (*api.ProfileReply, error) {
	return decoded[api.ProfileReply](
		c.API.PutLayerProfile(
			ctx,
			name,
			&api.PutLayerProfileParams{XSpaceweave: api.PutLayerProfileParamsXSpaceweaveN1},
			profile,
		),
	)
}

func (c *Client) GetInfo(ctx context.Context) (*api.Info, error) {
	return decoded[api.Info](c.API.GetInfo(ctx))
}

// GetArtifact returns a streaming artifact; the caller must close Body.
func (c *Client) GetArtifact(
	ctx context.Context,
	id string,
	artifact api.GetFloorplanArtifactParamsArtifact,
) (*http.Response, error) {
	return checked(c.API.GetFloorplanArtifact(ctx, id, artifact))
}

// DownloadFile returns an attached file; the caller must close Body.
func (c *Client) DownloadFile(ctx context.Context, id, sha string) (*http.Response, error) {
	return checked(c.API.DownloadFloorplanFile(ctx, id, sha))
}

func (c *Client) GetRuntime(ctx context.Context) (*operator.Runtime, error) {
	return decoded[operator.Runtime](c.Operator.GetRuntime(ctx))
}
