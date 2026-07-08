package sqlite

const Dialect = "sqlite"

type Factory struct {
	path string
}

func NewFactory(path string) *Factory {
	return &Factory{path: path}
}

func (f *Factory) Dialect() string {
	return Dialect
}

func (f *Factory) Path() string {
	return f.path
}
