import { useState } from "react"
import { Search, X } from "lucide-react"
import { Input } from "@/components/ui/input"
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover"

type SearchHistoryInputProps = {
  value: string
  onChange: (value: string) => void
  onClear: () => void
  onSubmit: () => void
  onHistorySelect: (value: string) => void
  placeholder: string
  history?: string[]
}

export function SearchHistoryInput({
  value,
  onChange,
  onClear,
  onSubmit,
  onHistorySelect,
  placeholder,
  history = [],
}: SearchHistoryInputProps) {
  const [open, setOpen] = useState(false)
  const showHistory = open && history.length > 0

  return (
    <Popover open={showHistory} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={value}
            onChange={(event) => onChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key !== "Enter") return
              event.preventDefault()
              setOpen(false)
              onSubmit()
            }}
            onFocus={() => setOpen(true)}
            onClick={() => setOpen(true)}
            className="pl-9 pr-10"
            placeholder={placeholder}
            autoComplete="off"
          />
          {value.trim() ? (
            <button
              type="button"
              onClick={() => {
                onClear()
                setOpen(false)
              }}
              className="absolute right-2 top-1/2 inline-flex size-7 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground transition hover:bg-muted hover:text-foreground"
              aria-label="清除"
            >
              <X className="size-4" />
            </button>
          ) : null}
        </div>
      </PopoverAnchor>
      <PopoverContent
        align="start"
        sideOffset={6}
        className="w-[var(--radix-popover-trigger-width)] p-1"
        onOpenAutoFocus={(event) => event.preventDefault()}
      >
        <div className="px-2 py-1.5 text-[11px] font-medium text-muted-foreground">最近查询</div>
        <div className="max-h-56 overflow-y-auto">
          {history.map((item, index) => (
            <button
              key={`${item}-${index}`}
              type="button"
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => {
                onChange(item)
                setOpen(false)
                onHistorySelect(item)
              }}
              className="block w-full truncate rounded-sm px-2 py-1.5 text-left text-sm transition hover:bg-accent hover:text-accent-foreground"
              title={item}
            >
              {item}
            </button>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  )
}
