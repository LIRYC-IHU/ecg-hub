package dicom

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	legacydicom "github.com/apaladiychuk/go-dicom"
	"github.com/apaladiychuk/go-dicom/dicomtag"
	netdicom "github.com/apaladiychuk/go-netdicom"
	"github.com/apaladiychuk/go-netdicom/sopclass"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
)

// SCUClient is a DICOM Service Class User that sends C-ECHO and C-STORE
// requests to a remote PACS.
type SCUClient struct {
	host      string
	port      int
	callingAE string
	calledAE  string
	timeout   time.Duration
	strictSOP bool
}

// NewSCUClient creates a client from connector config. Returns an error if
// the timeout duration is unparseable.
func NewSCUClient(cfg config.DICOMConnectorConfig) (*SCUClient, error) {
	timeout := 30 * time.Second
	if cfg.Timeout != "" {
		d, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("dicom: invalid timeout %q: %w", cfg.Timeout, err)
		}
		timeout = d
	}

	callingAE := cfg.CallingAE
	if callingAE == "" {
		callingAE = "ECGHUB"
	}
	calledAE := cfg.CalledAE
	if calledAE == "" {
		calledAE = "ANY-SCP"
	}

	return &SCUClient{
		host:      cfg.Host,
		port:      cfg.Port,
		callingAE: callingAE,
		calledAE:  calledAE,
		timeout:   timeout,
		strictSOP: cfg.StrictSOP,
	}, nil
}

func (c *SCUClient) addr() string {
	return fmt.Sprintf("%s:%d", c.host, c.port)
}

// Echo sends a C-ECHO to the remote PACS (DICOM verification).
// Returns nil if the peer responds with Success.
func (c *SCUClient) Echo(ctx context.Context) error {
	su, err := netdicom.NewServiceUser(netdicom.ServiceUserParams{
		CalledAETitle:  c.calledAE,
		CallingAETitle: c.callingAE,
		SOPClasses:     sopclass.VerificationClasses,
	})
	if err != nil {
		return fmt.Errorf("dicom: echo: create SCU: %w", err)
	}

	su.Connect(c.addr())

	type result struct {
		err error
	}
	ch := make(chan result, 1)
	go func() {
		ch <- result{err: su.CEcho()}
	}()

	select {
	case <-ctx.Done():
		su.Release()
		return fmt.Errorf("dicom: echo: %w", ctx.Err())
	case r := <-ch:
		su.Release()
		if r.err != nil {
			return fmt.Errorf("dicom: echo: %w", r.err)
		}
		return nil
	}
}

// Store sends a DICOM file to the remote PACS via C-STORE.
// The file is read from disk at filePath.
func (c *SCUClient) Store(ctx context.Context, filePath string) error {
	ds, err := legacydicom.ReadDataSetFromFile(filePath, legacydicom.ReadOptions{})
	if err != nil {
		return fmt.Errorf("dicom: store: read file %q: %w", filePath, err)
	}

	sopClassUID, err := c.extractSOPClassUID(ds)
	if err != nil {
		return err
	}

	if c.strictSOP && !IsKnownECGSOPClass(sopClassUID) {
		return fmt.Errorf("dicom: store: unknown SOP class %s (strict mode)", sopClassUID)
	}
	if !IsKnownECGSOPClass(sopClassUID) {
		slog.Warn("dicom: unknown SOP class, attempting best-effort forward",
			"sop_class", sopClassUID, "file", filePath)
	}

	sopClasses := c.buildSOPClasses(sopClassUID)

	su, err := netdicom.NewServiceUser(netdicom.ServiceUserParams{
		CalledAETitle:  c.calledAE,
		CallingAETitle: c.callingAE,
		SOPClasses:     sopClasses,
	})
	if err != nil {
		return fmt.Errorf("dicom: store: create SCU: %w", err)
	}

	su.Connect(c.addr())

	type result struct {
		err error
	}
	ch := make(chan result, 1)
	go func() {
		ch <- result{err: su.CStore(ds)}
	}()

	start := time.Now()
	select {
	case <-ctx.Done():
		su.Release()
		return fmt.Errorf("dicom: store: %w", ctx.Err())
	case r := <-ch:
		su.Release()
		dur := time.Since(start)
		if r.err != nil {
			slog.Warn("dicom: c-store failed",
				"sop_class", sopClassUID,
				"file", filePath,
				"duration_ms", dur.Milliseconds(),
				"error", r.err,
			)
			return fmt.Errorf("dicom: store: %w", r.err)
		}
		slog.Info("dicom: c-store success",
			"sop_class", sopClassUID,
			"file", filePath,
			"duration_ms", dur.Milliseconds(),
		)
		return nil
	}
}

func (c *SCUClient) extractSOPClassUID(ds *legacydicom.DataSet) (string, error) {
	elem, err := ds.FindElementByTag(dicomtag.MediaStorageSOPClassUID)
	if err != nil {
		elem, err = ds.FindElementByTag(dicomtag.SOPClassUID)
		if err != nil {
			return "", fmt.Errorf("dicom: store: no SOP Class UID in file: %w", err)
		}
	}
	uid, err := elem.GetString()
	if err != nil {
		return "", fmt.Errorf("dicom: store: bad SOP Class UID value: %w", err)
	}
	return uid, nil
}

// buildSOPClasses returns the list of SOP Class UIDs to propose in the association.
// Always includes Verification + the file's own SOP class + all known ECG classes.
func (c *SCUClient) buildSOPClasses(fileSOPClass string) []string {
	seen := make(map[string]bool)
	var classes []string

	add := func(uid string) {
		if !seen[uid] {
			seen[uid] = true
			classes = append(classes, uid)
		}
	}

	add(VerificationSOPClassUID)
	add(fileSOPClass)
	for _, uid := range KnownECGSOPClasses {
		add(uid)
	}
	return classes
}
