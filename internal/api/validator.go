package api

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"executor/internal/languages"
	"executor/internal/models"
)

// validate enforces the submission rules from ARCHITECTURE.md section 5.1.
func (s *Server) validate(req models.SubmissionRequest) error {
	if req.Language == "" || !languages.IsSupported(req.Language) {
		return fmt.Errorf("unsupported language %q", req.Language)
	}
	if req.Code == "" {
		return errors.New("code is required")
	}
	if len(req.Code) > s.cfg.MaxCodeSize {
		return fmt.Errorf("code exceeds %d bytes", s.cfg.MaxCodeSize)
	}
	if !utf8.ValidString(req.Code) {
		return errors.New("code must be valid UTF-8")
	}
	if strings.ContainsRune(req.Code, '\x00') {
		return errors.New("code contains null bytes")
	}
	if len(req.Stdin) > s.cfg.MaxStdinSize {
		return fmt.Errorf("stdin exceeds %d bytes", s.cfg.MaxStdinSize)
	}
	return nil
}
