import { useEffect, useState } from "react"

/**
 * Defers a value until it remains unchanged for the configured delay.
 * Consumers can compare both values to pause remote queries while the user is typing.
 */
export function useDebouncedValue<T>(value: T, delayMs = 300) {
  const [debouncedValue, setDebouncedValue] = useState(value)

  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedValue(value), delayMs)
    return () => window.clearTimeout(timer)
  }, [delayMs, value])

  return debouncedValue
}
