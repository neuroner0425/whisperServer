package app

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/store"
	intutil "whisperserver/src/internal/util"
)

func apiTagsJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	tags, err := store.ListTagsByOwner(u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}
	return c.JSON(http.StatusOK, map[string]any{"tags": tags})
}

func apiCreateTagJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}
	name := strings.TrimSpace(body.Name)
	desc := strings.TrimSpace(body.Description)
	if !intutil.IsValidTagName(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "태그명은 공백 없이 문자/숫자/_ 만 사용할 수 있습니다.")
	}
	if desc == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "태그 설명을 입력하세요.")
	}
	if err := store.UpsertTag(u.ID, name, desc); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 저장 실패")
	}
	return c.JSON(http.StatusOK, map[string]string{"name": name, "description": desc})
}

func apiDeleteTagJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "삭제할 태그가 없습니다.")
	}
	if err := store.DeleteTag(u.ID, name); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 삭제 실패")
	}
	removeTagFromOwnerJobs(u.ID, name)
	return c.JSON(http.StatusOK, map[string]string{"name": name, "status": "deleted"})
}
