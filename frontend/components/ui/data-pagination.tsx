import { ChevronLeftIcon, ChevronRightIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import {
  Pagination,
  PaginationContent,
  PaginationEllipsis,
  PaginationItem,
  PaginationLink,
} from "@/components/ui/pagination"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { cn } from "@/lib/utils"

const DEFAULT_PAGE_SIZE_OPTIONS = [20, 50, 100, 200]

type PageToken = number | "ellipsis"

function clampPage(page: number, pages: number) {
  return Math.min(Math.max(page, 1), Math.max(pages, 1))
}

function pageTokens(page: number, pages: number): PageToken[] {
  const current = clampPage(page, pages)
  if (pages <= 7) return Array.from({ length: pages }, (_, index) => index + 1)

  const visible = new Set([1, pages, current, current - 1, current + 1])
  if (current <= 3) {
    visible.add(2)
    visible.add(3)
    visible.add(4)
  }
  if (current >= pages - 2) {
    visible.add(pages - 1)
    visible.add(pages - 2)
    visible.add(pages - 3)
  }

  const ordered = Array.from(visible)
    .filter((item) => item >= 1 && item <= pages)
    .sort((a, b) => a - b)

  const tokens: PageToken[] = []
  for (const item of ordered) {
    const previous = tokens[tokens.length - 1]
    if (typeof previous === "number" && item - previous > 1) {
      tokens.push("ellipsis")
    }
    tokens.push(item)
  }
  return tokens
}

export interface DataPaginationProps {
  page: number
  pageSize: number
  pages: number
  total: number
  pageSizeOptions?: number[]
  disabled?: boolean
  className?: string
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
}

export function DataPagination({
  page,
  pageSize,
  pages,
  total,
  pageSizeOptions = DEFAULT_PAGE_SIZE_OPTIONS,
  disabled,
  className,
  onPageChange,
  onPageSizeChange,
}: DataPaginationProps) {
  const safePages = Math.max(pages, 1)
  const current = clampPage(page, safePages)
  const options = pageSizeOptions.includes(pageSize)
    ? pageSizeOptions
    : [...pageSizeOptions, pageSize].sort((a, b) => a - b)

  function go(nextPage: number) {
    const target = clampPage(nextPage, safePages)
    if (!disabled && target !== current) onPageChange(target)
  }

  return (
    <div
      className={cn(
        "flex flex-col gap-3 border-t border-border p-3 text-xs text-muted-foreground lg:flex-row lg:items-center lg:justify-between",
        className,
      )}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="whitespace-nowrap">共 {total} 条</span>
        <span className="whitespace-nowrap">第 {current} / {safePages} 页</span>
        <div className="flex items-center gap-2">
          <span className="whitespace-nowrap">每页</span>
          <Select
            value={String(pageSize)}
            onValueChange={(value) => onPageSizeChange(Number(value))}
            disabled={disabled}
          >
            <SelectTrigger className="h-8 w-[88px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {options.map((option) => (
                <SelectItem key={option} value={String(option)}>
                  {option}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <span className="whitespace-nowrap">条</span>
        </div>
      </div>

      <Pagination className="mx-0 justify-start lg:justify-end">
        <PaginationContent className="flex-wrap">
          <PaginationItem>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-8 gap-1 px-2.5"
              disabled={disabled || current <= 1}
              onClick={() => go(current - 1)}
            >
              <ChevronLeftIcon className="size-4" />
              <span className="hidden sm:inline">上一页</span>
            </Button>
          </PaginationItem>
          {pageTokens(current, safePages).map((token, index) => (
            <PaginationItem key={`${token}-${index}`}>
              {token === "ellipsis" ? (
                <PaginationEllipsis className="size-8" />
              ) : (
                <PaginationLink
                  href="#"
                  size="icon"
                  isActive={token === current}
                  className="size-8"
                  onClick={(event) => {
                    event.preventDefault()
                    go(token)
                  }}
                >
                  {token}
                </PaginationLink>
              )}
            </PaginationItem>
          ))}
          <PaginationItem>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-8 gap-1 px-2.5"
              disabled={disabled || current >= safePages}
              onClick={() => go(current + 1)}
            >
              <span className="hidden sm:inline">下一页</span>
              <ChevronRightIcon className="size-4" />
            </Button>
          </PaginationItem>
        </PaginationContent>
      </Pagination>
    </div>
  )
}
