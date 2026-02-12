package types

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationErrors []FieldError

func (v ValidationErrors) Error() string {
	return "validation failed"
}

func (v *ValidationErrors) Add(field, message string) {
	*v = append(*v, FieldError{
		Field:   field,
		Message: message,
	})
}

func (v ValidationErrors) HasErrors() bool {
	return len(v) > 0
}
