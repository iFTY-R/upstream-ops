package storage

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SavedSearchConditions struct{ db *gorm.DB }

func NewSavedSearchConditions(db *gorm.DB) *SavedSearchConditions {
	return &SavedSearchConditions{db: db}
}

func IsSavedSearchConditionField(field SavedSearchConditionField) bool {
	for _, valid := range SavedSearchConditionFields {
		if field == valid {
			return true
		}
	}
	return false
}

func NormalizeSavedSearchConditionValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (r *SavedSearchConditions) List(fields []SavedSearchConditionField) ([]SavedSearchCondition, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("saved search conditions repository is not configured")
	}
	if len(fields) == 0 {
		fields = SavedSearchConditionFields
	}
	for _, field := range fields {
		if !IsSavedSearchConditionField(field) {
			return nil, fmt.Errorf("invalid saved search condition field: %s", field)
		}
	}

	var list []SavedSearchCondition
	if err := r.db.
		Where("field IN ?", fields).
		Order("updated_at DESC").
		Order("id DESC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *SavedSearchConditions) Save(field SavedSearchConditionField, value string) (*SavedSearchCondition, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("saved search conditions repository is not configured")
	}
	if !IsSavedSearchConditionField(field) {
		return nil, fmt.Errorf("invalid saved search condition field: %s", field)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("saved search condition value is required")
	}
	normalized := NormalizeSavedSearchConditionValue(value)
	now := time.Now()
	condition := SavedSearchCondition{
		Field:           field,
		Value:           value,
		NormalizedValue: normalized,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "field"}, {Name: "normalized_value"}},
		DoUpdates: clause.Assignments(map[string]any{
			"value":      value,
			"updated_at": now,
		}),
	}).Create(&condition).Error; err != nil {
		return nil, err
	}
	return r.FindByFieldValue(field, normalized)
}

func (r *SavedSearchConditions) FindByFieldValue(field SavedSearchConditionField, normalizedValue string) (*SavedSearchCondition, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("saved search conditions repository is not configured")
	}
	var condition SavedSearchCondition
	if err := r.db.
		Where("field = ? AND normalized_value = ?", field, normalizedValue).
		First(&condition).Error; err != nil {
		return nil, err
	}
	return &condition, nil
}

func (r *SavedSearchConditions) Delete(id uint) error {
	if r == nil || r.db == nil {
		return errors.New("saved search conditions repository is not configured")
	}
	if id == 0 {
		return errors.New("saved search condition id is required")
	}
	return r.db.Delete(&SavedSearchCondition{}, id).Error
}
