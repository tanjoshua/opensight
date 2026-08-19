import type { Competitor } from "@/gen/opensight/v1/competitor_pb"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { FieldError } from "@/components/ui/field"

// Confirms merging a competitor row into the business. The merge rewrites past
// mentions and cannot be undone from the UI, so it always asks first.
export function ClaimSelfDialog({
  competitor,
  businessName,
  onOpenChange,
  submitting,
  errorMessage,
  onConfirm,
}: {
  competitor?: Competitor
  businessName: string
  onOpenChange: (open: boolean) => void
  submitting: boolean
  errorMessage?: string
  onConfirm: () => void
}) {
  return (
    <Dialog open={competitor !== undefined} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Is this your business?</DialogTitle>
          <DialogDescription>
            “{competitor?.name}” becomes an alias of {businessName}. Its past
            mentions count towards your own coverage instead of a competitor’s,
            and future responses match it to you automatically. This cannot be
            undone.
          </DialogDescription>
        </DialogHeader>

        {errorMessage && <FieldError>{errorMessage}</FieldError>}

        <DialogFooter>
          <Button
            variant="outline"
            disabled={submitting}
            onClick={() => onOpenChange(false)}
          >
            Cancel
          </Button>
          <Button disabled={submitting} onClick={onConfirm}>
            Yes, this is my business
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
