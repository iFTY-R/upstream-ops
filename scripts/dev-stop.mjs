#!/usr/bin/env node

import { parseCommonArgs, stopDev } from "./dev-common.mjs"

try {
  await stopDev(parseCommonArgs(process.argv.slice(2)))
} catch (error) {
  console.error(error instanceof Error ? error.message : error)
  process.exit(1)
}
