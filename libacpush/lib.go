// Copyright 2015 CoreOS, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package libacpush

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/appc/acpush/Godeps/_workspace/src/github.com/appc/spec/aci"
	"github.com/appc/acpush/Godeps/_workspace/src/github.com/appc/spec/discovery"
	"github.com/appc/acpush/Godeps/_workspace/src/github.com/coreos/ioprogress"
)

type initiateDetails struct {
	ACIPushVersion string `json:"aci_push_version"`
	Multipart      bool   `json:"multipart"`
	ManifestURL    string `json:"upload_manifest_url"`
	SignatureURL   string `json:"upload_signature_url"`
	ACIURL         string `json:"upload_aci_url"`
	CompletedURL   string `json:"completed_url"`
}

type completeMsg struct {
	Success      bool   `json:"success"`
	Reason       string `json:"reason,omitempty"`
	ServerReason string `json:"server_reason,omitempty"`
}

// stolen from github.com/coreos/rkt/rkt/rkt.go
func stderr(format string, a ...interface{}) {
	out := fmt.Sprintf(format, a...)
	fmt.Fprintln(os.Stderr, strings.TrimSuffix(out, "\n"))
}

// Uploader holds information about an upload to be performed.
type Uploader struct {
	Acipath  string
	Ascpath  string
	Uri      string
	Insecure bool
	Debug    bool

	// SetHTTPHeaders is called on every request before being sent.
	// This is exposed so that the user of acpush can set any headers
	// necessary for authentication.
	SetHTTPHeaders func(*http.Request)
}

// Perform the upload of the ACI and signature specified in the Uploader struct.
func (u Uploader) Upload() error {
	app, err := discovery.NewAppFromString(u.Uri)
	if err != nil {
		return err
	}

	if _, ok := app.Labels["arch"]; !ok {
		app.Labels["arch"] = "amd64"
	}

	if _, ok := app.Labels["os"]; !ok {
		app.Labels["os"] = "linux"
	}

	if _, ok := app.Labels["ext"]; !ok {
		app.Labels["ext"] = "aci"
	}

	acifile, err := os.Open(u.Acipath)
	if err != nil {
		return err
	}
	defer acifile.Close()

	ascfile, err := os.Open(u.Ascpath)
	if err != nil {
		return err
	}
	defer ascfile.Close()

	manifest, err := aci.ManifestFromImage(acifile)
	if err != nil {
		return err
	}

	// Just to make sure that we start reading from the front of the file in
	// case aci.ManifestFromImage changed the cursor into the file.
	_, err = acifile.Seek(0, 0)
	if err != nil {
		return err
	}

	manblob, err := manifest.MarshalJSON()
	if err != nil {
		return err
	}

	initurl, err := u.getInitiationURL(app)
	if err != nil {
		return err
	}

	initDeets, err := u.initiateUpload(initurl)
	if err != nil {
		return err
	}

	err = u.uploadPart(initDeets.ManifestURL, bytes.NewReader(manblob), false, nil)
	if err != nil {
		reason := fmt.Sprintf("error uploading manifest: %v", err)
		reportErr := u.reportFailure(initDeets.CompletedURL, reason)
		if reportErr != nil {
			return fmt.Errorf("error uploading manifest and error reporting failure: %v, %v", err, reportErr)
		}
		return err
	}

	label := "signature"
	err = u.uploadPart(initDeets.SignatureURL, ascfile, true, &label)
	if err != nil {
		reason := fmt.Sprintf("error uploading signature: %v", err)
		reportErr := u.reportFailure(initDeets.CompletedURL, reason)
		if reportErr != nil {
			return fmt.Errorf("error uploading manifest and error reporting failure: %v, %v", err, reportErr)
		}
		return err
	}

	label = "ACI"
	err = u.uploadPart(initDeets.ACIURL, acifile, true, &label)
	if err != nil {
		reason := fmt.Sprintf("error uploading aci: %v", err)
		reportErr := u.reportFailure(initDeets.CompletedURL, reason)
		if reportErr != nil {
			return fmt.Errorf("error uploading manifest and error reporting failure: %v, %v", err, reportErr)
		}
		return err
	}

	err = u.reportSuccess(initDeets.CompletedURL)
	if err != nil {
		return err
	}

	return nil
}

func (u Uploader) getInitiationURL(app *discovery.App) (string, error) {
	if u.Debug {
		stderr("searching for push endpoint via meta discovery")
	}
	eps, attempts, err := discovery.DiscoverEndpoints(*app, u.Insecure)
	if u.Debug {
		for _, a := range attempts {
			stderr("meta tag 'ac-push-discovery' not found on %s: %v", a.Prefix, a.Error)
		}
	}
	if err != nil {
		return "", err
	}
	if len(eps.ACIPushEndpoints) == 0 {
		return "", fmt.Errorf("no endpoints discovered")
	}

	if u.Debug {
		stderr("push endpoint found: %s", eps.ACIPushEndpoints[0])
	}

	return eps.ACIPushEndpoints[0], nil
}

func (u Uploader) initiateUpload(initurl string) (*initiateDetails, error) {
	if u.Debug {
		stderr("initiating upload")
	}
	resp, err := u.performRequest("POST", initurl, nil, false, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	respblob, err := ioutil.ReadAll(resp)
	if err != nil {
		return nil, err
	}

	deets := &initiateDetails{}
	err = json.Unmarshal(respblob, deets)

	if u.Debug {
		stderr("upload initiated")
		stderr(" - manifest endpoint: %s", deets.ManifestURL)
		stderr(" - signature endpoint: %s", deets.SignatureURL)
		stderr(" - aci endpoint: %s", deets.ACIURL)
	}

	return deets, err
}

func (u Uploader) uploadPart(url string, body io.Reader, draw bool, label *string) error {
	resp, err := u.performRequest("PUT", url, body, draw, label)
	if err != nil {
		return err
	}
	resp.Close()
	return nil
}

func (u Uploader) reportSuccess(url string) error {
	respblob, err := json.Marshal(completeMsg{true, "", ""})
	if err != nil {
		return err
	}
	return u.complete(url, respblob)
}

func (u Uploader) reportFailure(url string, reason string) error {
	respblob, err := json.Marshal(completeMsg{false, reason, ""})
	if err != nil {
		return err
	}
	return u.complete(url, respblob)
}

func (u Uploader) complete(url string, blob []byte) error {
	resp, err := u.performRequest("POST", url, bytes.NewReader(blob), false, nil)
	if err != nil {
		return err
	}
	defer resp.Close()

	respblob, err := ioutil.ReadAll(resp)
	if err != nil {
		return err
	}

	reply := &completeMsg{}
	err = json.Unmarshal(respblob, reply)
	if err != nil {
		return err
	}

	if !reply.Success {
		return fmt.Errorf("%s", reply.ServerReason)
	}

	return nil
}

func (u Uploader) performRequest(reqType string, url string, body io.Reader, draw bool, label *string) (io.ReadCloser, error) {
	if fbody, ok := body.(*os.File); draw && ok {
		finfo, err := fbody.Stat()
		if err != nil {
			return nil, err
		}
		if u.Debug {
			var prefix string
			if label != nil {
				prefix = "Uploading " + *label
			} else {
				prefix = "Uploading"
			}
			fmtBytesSize := 18
			barSize := int64(80 - len(prefix) - fmtBytesSize)
			bar := ioprogress.DrawTextFormatBarForW(barSize, os.Stderr)
			fmtfunc := func(progress, total int64) string {
				// Content-Length is set to -1 when unknown.
				if total == -1 {
					return fmt.Sprintf(
						"%s: %v of an unknown total size",
						prefix,
						ioprogress.ByteUnitStr(progress),
					)
				}
				return fmt.Sprintf(
					"%s: %s %s",
					prefix,
					bar(progress, total),
					ioprogress.DrawTextFormatBytes(progress, total),
				)
			}
			body = &ioprogress.Reader{
				Reader:       fbody,
				Size:         finfo.Size(),
				DrawFunc:     ioprogress.DrawTerminalf(os.Stderr, fmtfunc),
				DrawInterval: time.Second,
			}
		}
	}

	req, err := http.NewRequest(reqType, url, body)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport
	if u.Insecure {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	u.SetHTTPHeaders(req)

	client := &http.Client{Transport: transport}

	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		u.SetHTTPHeaders(req)
		return nil
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	switch res.StatusCode {
	case http.StatusOK:
		return res.Body, nil
	case http.StatusBadRequest:
		return res.Body, nil
	default:
		res.Body.Close()
		return nil, fmt.Errorf("bad HTTP status code: %d", res.StatusCode)
	}

}
