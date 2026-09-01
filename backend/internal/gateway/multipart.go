package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"
)

type multipartLogFile struct {
	Name        string `json:"name"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
}

type multipartLogRequest struct {
	ContentType string             `json:"content_type"`
	Fields      map[string]string  `json:"fields"`
	Files       []multipartLogFile `json:"files,omitempty"`
}

func parseMultipartModel(body []byte, contentType string) (model string, logJSON string, err error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return "", "", errInvalidMultipart
	}
	boundary := params["boundary"]
	if boundary == "" {
		return "", "", errInvalidMultipart
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	log := multipartLogRequest{ContentType: mediaType, Fields: map[string]string{}}
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return "", "", nextErr
		}
		payload, readErr := io.ReadAll(part)
		_ = part.Close()
		if readErr != nil {
			return "", "", readErr
		}
		name := strings.TrimSpace(part.FormName())
		filename := strings.TrimSpace(part.FileName())
		if filename != "" {
			log.Files = append(log.Files, multipartLogFile{
				Name:        name,
				Filename:    filename,
				ContentType: part.Header.Get("Content-Type"),
				Size:        int64(len(payload)),
			})
			continue
		}
		if name != "" {
			log.Fields[name] = string(payload)
			if name == "model" {
				model = strings.TrimSpace(string(payload))
			}
		}
	}
	encoded, _ := json.Marshal(log)
	return model, string(encoded), nil
}

var errInvalidMultipart = errString("invalid multipart body")

type errString string

func (e errString) Error() string { return string(e) }
