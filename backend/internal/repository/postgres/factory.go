package postgres

const Dialect = "postgres"

type Factory struct {
	databaseURL string
}

func NewFactory(databaseURL string) *Factory {
	return &Factory{databaseURL: databaseURL}
}

func (f *Factory) Dialect() string {
	return Dialect
}

func (f *Factory) DatabaseURL() string {
	return f.databaseURL
}
