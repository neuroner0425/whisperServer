package httptransport

func PaginateJobRows(rows []JobRow, page, pageSize int) ([]JobRow, int, int) {
	if pageSize <= 0 {
		pageSize = 20
	}
	totalPages := (len(rows) + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(rows) {
		start = len(rows)
	}
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], page, totalPages
}
