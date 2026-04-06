// tag_service.go contains tag validation and mutation rules shared by handlers.
package service

import (
	"net/http"
	"strings"

	model "whisperserver/src/internal/domain"
)

type TagServiceDeps struct {
	ListTagsByOwner     func(ownerID string) ([]model.Tag, error)
	UpsertTag           func(ownerID, name, desc string) error
	DeleteTag           func(ownerID, name string) error
	ListTagNamesByOwner func(ownerID string) (map[string]struct{}, error)
}

type TagService struct {
	d TagServiceDeps
}

// NewTagService builds the tag service from repo callbacks.
func NewTagService(d TagServiceDeps) *TagService {
	return &TagService{d: d}
}

// List returns every tag owned by the user.
func (s *TagService) List(ownerID string) ([]model.Tag, error) {
	if s.d.ListTagsByOwner == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	tags, err := s.d.ListTagsByOwner(ownerID)
	if err != nil {
		return nil, NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}
	return tags, nil
}

// Upsert validates and creates or updates a tag.
func (s *TagService) Upsert(ownerID, name, desc string, isValidName func(string) bool) error {
	name = strings.TrimSpace(name)
	desc = strings.TrimSpace(desc)
	if isValidName == nil || !isValidName(name) {
		return NewHTTPError(http.StatusBadRequest, "태그명은 공백 없이 문자/숫자/_ 만 사용할 수 있습니다.")
	}
	if desc == "" {
		return NewHTTPError(http.StatusBadRequest, "태그 설명을 입력하세요.")
	}
	if s.d.UpsertTag == nil {
		return NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	if err := s.d.UpsertTag(ownerID, name, desc); err != nil {
		return NewHTTPError(http.StatusInternalServerError, "태그 저장 실패")
	}
	return nil
}

// Delete removes a tag by name.
func (s *TagService) Delete(ownerID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return NewHTTPError(http.StatusBadRequest, "삭제할 태그가 없습니다.")
	}
	if s.d.DeleteTag == nil {
		return NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	if err := s.d.DeleteTag(ownerID, name); err != nil {
		return NewHTTPError(http.StatusInternalServerError, "태그 삭제 실패")
	}
	return nil
}

// ValidateOwnedTags keeps only tags that exist for the current owner.
func (s *TagService) ValidateOwnedTags(ownerID string, tags []string) ([]string, error) {
	if s.d.ListTagNamesByOwner == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	allowed, err := s.d.ListTagNamesByOwner(ownerID)
	if err != nil {
		return nil, NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}
	validated := make([]string, 0, len(tags))
	seen := map[string]struct{}{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := allowed[t]; !ok {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		validated = append(validated, t)
	}
	return validated, nil
}
