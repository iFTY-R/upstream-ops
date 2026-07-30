#!/usr/bin/env node

import { parseCommonArgs, startDev } from "./dev-common.mjs"

try {
  await startDev(parseCommonArgs(process.argv.slice(2)))
} catch (error) {
  console.error(error instanceof Error ? error.message : error)
  process.exit(1)
}
