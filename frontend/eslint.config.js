import js from "@eslint/js"
import tseslint from "typescript-eslint"
import reactHooks from "eslint-plugin-react-hooks"
import globals from "globals"

export default tseslint.config(
  { ignores: ["dist", "node_modules"] },
  {
    files: ["**/*.{ts,tsx}"],
    extends: [
      js.configs.recommended,
      ...tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
    ],
    languageOptions: {
      globals: globals.browser,
    },
    rules: {
      // 现有代码大量用 any 兜底外部 JSON；类型收紧另行处理，不放进 lint 门禁。
      "@typescript-eslint/no-explicit-any": "off",
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_", caughtErrors: "none" },
      ],
      // 依赖数组问题需要逐个人工确认，先以 warn 呈现，不阻塞 CI。
      "react-hooks/exhaustive-deps": "warn",
      // v7 新增的 React Compiler 系规则命中的是现有可运行代码的模式
      // （自定义 useApi 数据层在 effect 里 setState、渲染期读 ref/Date.now 等）。
      // 逐步偿还，先降为 warn；rules-of-hooks 保持 error。
      "react-hooks/set-state-in-effect": "warn",
      "react-hooks/refs": "warn",
      "react-hooks/purity": "warn",
      "react-hooks/immutability": "warn",
    },
  },
  {
    files: ["**/*.test.ts"],
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
    },
  },
)
