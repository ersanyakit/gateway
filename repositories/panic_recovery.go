package repositories

import (
	"errors"
	"fmt"
	"log"
	"runtime/debug"

	"gorm.io/gorm"
)

func repositoryOperationError(operation, message string) error {
	err := fmt.Errorf("%s: %s", operation, message)
	log.Printf("repository operation=%s error=%v", operation, err)
	return err
}

func recoverRepositoryTransactionPanic(operation string, tx *gorm.DB, recovered any) error {
	var recoveredErr error
	if panicErr, ok := recovered.(error); ok {
		recoveredErr = fmt.Errorf("%s panic recovered: %w", operation, panicErr)
	} else {
		recoveredErr = fmt.Errorf("%s panic recovered: %v", operation, recovered)
	}

	var rollbackErr error
	if tx != nil {
		rollbackErr = tx.Rollback().Error
	}
	if rollbackErr != nil {
		recoveredErr = errors.Join(
			recoveredErr,
			fmt.Errorf("%s transaction rollback failed: %w", operation, rollbackErr),
		)
	}

	log.Printf(
		"repository operation=%s recovered_panic_type=%T recovered_panic=%v rollback_error=%v\n%s",
		operation,
		recovered,
		recovered,
		rollbackErr,
		debug.Stack(),
	)
	return recoveredErr
}
