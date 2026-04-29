package tfrun

type DataType string

const (
	DATA_TYPE_STRING       = "STRING"
	DATA_TYPE_INTEGER      = "INTEGER"
	DATA_TYPE_BOOLEAN      = "BOOLEAN"
	DATA_TYPE_CODE         = "CODE"
	DATA_TYPE_SINGLESELECT = "SINGLE_SELECT"
	DATA_TYPE_MULTISELECT  = "MULTI_SELECT"
	// This is used not for Terraform but rather if a file is delivered for
	// consumption during runtime.
	DATA_TYPE_FILE = "FILE"

	// legacy, remove once lists are no more
	DATA_TYPE_LIST = "LIST"
)
