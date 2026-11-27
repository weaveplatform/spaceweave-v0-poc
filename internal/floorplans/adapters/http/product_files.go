package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/weaveplatform/spaceweave/internal/floorplans/api"
	transport "github.com/weaveplatform/spaceweave/internal/transport/gen/api"
)

func (p productServer) receive(
	reader *multipart.Reader,
	field string,
) (*received, productResponse) {
	dir, err := os.MkdirTemp("", "spaceweave-v1-upload-")
	if err != nil {
		return nil, p.failure(err)
	}
	got := &received{dir: dir, fields: make(map[string]string)}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			got.close()
			return nil, productReadFailure(err)
		}
		if problem := readProductPart(got, part, field); problem.status != 0 {
			got.close()
			return nil, problem
		}
	}
	if got.path == "" {
		got.close()
		return nil, productProblem(
			http.StatusBadRequest,
			"The multipart request is missing its file.",
		)
	}
	if code, detail := check(got.path); detail != "" {
		got.close()
		return nil, productProblem(code, detail)
	}
	return got, productResponse{}
}

func readProductPart(got *received, part *multipart.Part, field string) productResponse {
	name := part.FormName()
	if name == field && got.path == "" && part.FileName() != "" {
		base := filepath.Base(part.FileName())
		if strings.HasPrefix(base, ".") || strings.ContainsAny(base, "/\\\x00\r\n") {
			return productProblem(http.StatusBadRequest, "The file needs a valid name.")
		}
		got.path = filepath.Join(got.dir, base)
		if err := save(got.path, part); err != nil {
			return productReadFailure(err)
		}
		return productResponse{}
	}
	if field == "file" && name == "role" && part.FileName() == "" {
		if _, exists := got.fields[name]; exists {
			return productProblem(http.StatusBadRequest, "The multipart role must occur once.")
		}
		data, err := io.ReadAll(io.LimitReader(part, maxField+1))
		if err != nil {
			return productReadFailure(err)
		}
		if len(data) > maxField {
			return productProblem(
				http.StatusRequestEntityTooLarge,
				"The multipart field is too large.",
			)
		}
		got.fields[name] = strings.TrimSpace(string(data))
		return productResponse{}
	}
	return productProblem(
		http.StatusBadRequest,
		"The multipart fields do not match the API contract.",
	)
}

func (p productServer) UploadFloorplan(
	_ context.Context,
	request transport.UploadFloorplanRequestObject,
) (transport.UploadFloorplanResponseObject, error) {
	got, problem := p.receive(request.Body, "image")
	if got == nil {
		return problem, nil
	}
	defer got.close()
	e, created, err := p.server.Store.Ingest(got.path)
	if err != nil {
		return p.failure(err), nil
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		p.server.wake()
	}
	return productJSON(status, uploadReply{Plan: productEntry(e), Duplicate: !created}), nil
}

func (p productServer) AttachFloorplanFile(
	_ context.Context,
	request transport.AttachFloorplanFileRequestObject,
) (transport.AttachFloorplanFileResponseObject, error) {
	got, problem := p.receive(request.Body, "file")
	if got == nil {
		return problem, nil
	}
	defer got.close()
	role := got.fields["role"]
	if role != api.RoleSpaces && role != api.RoleReference {
		return productProblem(
			http.StatusBadRequest,
			"An attachment role must be spaces or reference.",
		), nil
	}
	e, added, err := p.server.Store.Attach(request.Id, got.path, role)
	if err != nil {
		return p.failure(err), nil
	}
	status := http.StatusOK
	if added {
		status = http.StatusCreated
		p.server.requeued(e)
	}
	return productJSON(status, attachReply{Plan: productEntry(e), Duplicate: !added}), nil
}

func (p productServer) DetachFloorplanFile(
	_ context.Context,
	request transport.DetachFloorplanFileRequestObject,
) (transport.DetachFloorplanFileResponseObject, error) {
	e, err := p.server.Store.Detach(request.Id, request.Sha)
	if err != nil {
		return p.failure(err), nil
	}
	p.server.requeued(e)
	return productJSON(http.StatusOK, attachReply{Plan: productEntry(e)}), nil
}

func (p productServer) GetFloorplanArtifact(
	_ context.Context,
	request transport.GetFloorplanArtifactRequestObject,
) (transport.GetFloorplanArtifactResponseObject, error) {
	if !request.Artifact.Valid() {
		return productProblem(http.StatusBadRequest, "The artifact kind is invalid."), nil
	}
	if p.server.Assets == nil {
		return productProblem(http.StatusNotFound, "Artifact not found."), nil
	}
	asset, err := p.server.Assets.Artifact(request.Id, string(request.Artifact))
	if err != nil {
		return p.failure(err), nil
	}
	if request.Artifact == transport.Model || request.Artifact == transport.Report {
		defer asset.Reader.Close() //nolint:errcheck // read-only generated model
		var model map[string]json.RawMessage
		if err := json.NewDecoder(asset.Reader).Decode(&model); err != nil {
			return p.failure(err), nil
		}
		if request.Artifact == transport.Report {
			var detail string
			if err := json.Unmarshal(model["error"], &detail); err == nil && detail != "" {
				model["error"] = json.RawMessage(
					`"Processing failed. Retry or contact an administrator."`,
				)
			}
			return productJSON(http.StatusOK, model), nil
		}
		var source map[string]json.RawMessage
		if err := json.Unmarshal(model["SOURCE"], &source); err != nil || source == nil {
			return productProblem(
				http.StatusInternalServerError,
				"The generated model is unavailable.",
			), nil
		}
		source["url"], _ = json.Marshal(
			productPrefix + "/floorplans/" + url.PathEscape(request.Id) + "/artifacts/source",
		)
		model["SOURCE"], _ = json.Marshal(source)
		return productJSON(http.StatusOK, model), nil
	}
	return productAsset(asset, request.Artifact == transport.Original), nil
}

func (p productServer) DownloadFloorplanFile(
	_ context.Context,
	request transport.DownloadFloorplanFileRequestObject,
) (transport.DownloadFloorplanFileResponseObject, error) {
	if p.server.Assets == nil {
		return productProblem(http.StatusNotFound, "Attachment not found."), nil
	}
	asset, err := p.server.Assets.Attachment(request.Id, request.Sha)
	if err != nil {
		return p.failure(err), nil
	}
	return productAsset(asset, true), nil
}

func productAsset(asset api.Asset, download bool) productResponse {
	disposition := "inline"
	if download {
		disposition = "attachment"
	}
	return productResponse{
		status: http.StatusOK, contentType: asset.ContentType, reader: asset.Reader,
		disposition: mime.FormatMediaType(disposition, map[string]string{"filename": asset.Name}),
	}
}
