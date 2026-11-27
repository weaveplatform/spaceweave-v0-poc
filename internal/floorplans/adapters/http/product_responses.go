package server

import "net/http"

func (p productResponse) VisitListFloorplansResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitUploadFloorplanResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitGetFloorplanResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitApproveFloorplanResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitGetFloorplanArtifactResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitAttachFloorplanFileResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitDetachFloorplanFileResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitDownloadFloorplanFileResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitGetFloorplanProcessingResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitRejectFloorplanResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitRetraceFloorplanResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitRetryFloorplanResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitGetInfoResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitGetLayerProfileResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p productResponse) VisitPutLayerProfileResponse(w http.ResponseWriter) error {
	return p.write(w)
}
