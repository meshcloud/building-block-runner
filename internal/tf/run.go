package tf

const (
	DEFAULT_TF_VER = "1.4.4"
)

type Variable struct {
	value any
	env   bool
	Type  DataType
}
