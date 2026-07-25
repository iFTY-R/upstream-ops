export type ChannelPageSizePreference = 9 | 18 | 36 | 72 | 81 | "all"

const channelCardsPreferencesKey = "upstream-ops:channel-cards-preferences:v1"

export const channelPageSizeOptions: ChannelPageSizePreference[] = [9, 18, 36, 72, 81, "all"]

const defaultChannelCardsPreferences: ChannelCardsPreferences = {
  pageSize: 9,
}

export interface ChannelCardsPreferences {
  pageSize: ChannelPageSizePreference
}

function readObject(key: string): Record<string, unknown> | null {
  if (typeof window === "undefined") return null
  try {
    const raw = window.localStorage.getItem(key)
    if (!raw) return null
    const value = JSON.parse(raw)
    return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null
  } catch {
    return null
  }
}

function writeObject(key: string, value: object) {
  if (typeof window === "undefined") return
  try {
    window.localStorage.setItem(key, JSON.stringify(value))
  } catch {
    // Local preference persistence must not block the main UI.
  }
}

export function asChannelPageSize(value: unknown): ChannelPageSizePreference {
  if (value === "all") return "all"
  return typeof value === "number" && channelPageSizeOptions.includes(value as ChannelPageSizePreference)
    ? value as ChannelPageSizePreference
    : defaultChannelCardsPreferences.pageSize
}

export function readChannelCardsPreferences(): ChannelCardsPreferences {
  const value = readObject(channelCardsPreferencesKey)
  if (!value) return { ...defaultChannelCardsPreferences }
  return {
    pageSize: asChannelPageSize(value.pageSize),
  }
}

export function writeChannelCardsPreferences(value: ChannelCardsPreferences) {
  writeObject(channelCardsPreferencesKey, value)
}
