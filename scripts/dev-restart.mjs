#!/usr/bin/env node

import { parseCommonArgs, startDev, stopDev } from "./dev-common.mjs"

try {
  const options = parseCommonArgs(process.argv.slice(2))
  await stopDev(options)
  await new Promise((resolve) => setTimeout(resolve, 1000))
  await startDev(options)
} catch (error) {
  console.error(error instanceof Error ? error.message : error)
  process.exit(1)
}
