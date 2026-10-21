package hub

import "crypto/subtle"

// validInvitation accepts the current joining secret. The code is consumed
// only after a control connection is admitted, not by download attempts.
func (s *Server) validInvitation(token []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && subtle.ConstantTimeCompare(token, s.code.Secret[:]) == 1
}

// validDownloadToken keeps each admitted user's file streams working when
// the next invitation is generated. Session access ends on control disconnect.
func (s *Server) validDownloadToken(token []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if !s.oneTimePad {
		return subtle.ConstantTimeCompare(token, s.code.Secret[:]) == 1
	}
	var key [16]byte
	copy(key[:], token)
	return s.sessions[key]
}
