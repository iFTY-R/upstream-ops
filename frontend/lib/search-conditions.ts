import { apiFetch } from "@/lib/api"
import type { SavedSearchCondition, SavedSearchConditionField } from "@/lib/api-types"

export function savedSearchConditionsForField(
  list: SavedSearchCondition[] | null | undefined,
  field: SavedSearchConditionField,
) {
  return (list ?? []).filter((item) => item.field === field)
}

export function saveSavedSearchCondition(field: SavedSearchConditionField, value: string) {
  return apiFetch<SavedSearchCondition>("/search-conditions", {
    method: "POST",
    body: JSON.stringify({ field, value }),
  })
}

export function deleteSavedSearchCondition(id: number) {
  return apiFetch<{ deleted: boolean }>(`/search-conditions/${id}`, { method: "DELETE" })
}
