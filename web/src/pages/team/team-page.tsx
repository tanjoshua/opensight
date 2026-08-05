import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { Plus, Trash2 } from "lucide-react"
import { useState, type FormEvent } from "react"
import { useParams } from "react-router"

import { errorMessage } from "@/api/errors"
import { useAccountContext, useMe } from "@/api/hooks"
import {
  addMember,
  listMembers,
  removeMember,
  updateMemberRole,
} from "@/gen/opensight/v1/account-AccountService_connectquery"
import { AccountRole, type AccountMember } from "@/gen/opensight/v1/account_pb"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

const ROLE_OPTIONS = [
  { value: AccountRole.ADMIN, label: "Admin" },
  { value: AccountRole.MEMBER, label: "Member" },
  { value: AccountRole.VIEWER, label: "Viewer" },
]

export function TeamPage() {
  const { accountSlug = "" } = useParams<{ accountSlug: string }>()
  const account = useAccountContext()
  const me = useMe()
  const members = useQuery(listMembers, { accountSlug })
  const queryClient = useQueryClient()
  const canManage =
    account.data?.role === AccountRole.OWNER ||
    account.data?.role === AccountRole.ADMIN

  async function refresh() {
    await queryClient.invalidateQueries()
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="font-heading text-2xl font-semibold">Members</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            People here can access this workspace and every business in it.
          </p>
        </div>
        {canManage && (
          <AddMemberDialog
            accountSlug={accountSlug}
            isOwner={account.data?.role === AccountRole.OWNER}
            onChanged={refresh}
          />
        )}
      </div>
      {members.isError ? (
        <p role="alert" className="text-sm text-destructive">
          {errorMessage(members.error, "Couldn't load team members.")}
        </p>
      ) : (
        <div className="overflow-hidden rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Person</TableHead>
                <TableHead>Role</TableHead>
                <TableHead className="w-16">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {members.data?.members.map((member) => (
                <MemberRow
                  key={member.userId}
                  member={member}
                  accountSlug={accountSlug}
                  currentUserId={me.data?.user?.id}
                  canManage={canManage}
                  isOwner={account.data?.role === AccountRole.OWNER}
                  onChanged={refresh}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  )
}

function MemberRow({
  member,
  accountSlug,
  currentUserId,
  canManage,
  isOwner,
  onChanged,
}: {
  member: AccountMember
  accountSlug: string
  currentUserId?: string
  canManage: boolean
  isOwner: boolean
  onChanged: () => Promise<void>
}) {
  const update = useMutation(updateMemberRole, { onSuccess: onChanged })
  const remove = useMutation(removeMember, { onSuccess: onChanged })
  const editable =
    canManage &&
    member.userId !== currentUserId &&
    (isOwner || member.role !== AccountRole.OWNER)
  const roles = isOwner
    ? [{ value: AccountRole.OWNER, label: "Owner" }, ...ROLE_OPTIONS]
    : ROLE_OPTIONS
  return (
    <TableRow>
      <TableCell>
        <div className="font-medium">{member.email}</div>
        {member.pending && (
          <Badge className="mt-1" variant="secondary">
            Pending sign-in
          </Badge>
        )}
      </TableCell>
      <TableCell>
        {editable ? (
          <Select
            value={String(member.role)}
            onValueChange={(role) =>
              update.mutate({
                accountSlug,
                userId: member.userId,
                role: Number(role) as AccountRole,
              })
            }
          >
            <SelectTrigger className="w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {roles.map((role) => (
                <SelectItem key={role.value} value={String(role.value)}>
                  {role.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          roleLabel(member.role)
        )}
      </TableCell>
      <TableCell>
        {editable && (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`Remove ${member.email}`}
            disabled={remove.isPending}
            onClick={() =>
              remove.mutate({ accountSlug, userId: member.userId })
            }
          >
            <Trash2 />
          </Button>
        )}
      </TableCell>
    </TableRow>
  )
}

function AddMemberDialog({
  accountSlug,
  isOwner,
  onChanged,
}: {
  accountSlug: string
  isOwner: boolean
  onChanged: () => Promise<void>
}) {
  const [open, setOpen] = useState(false)
  const [email, setEmail] = useState("")
  const [role, setRole] = useState<AccountRole>(AccountRole.MEMBER)
  const mutation = useMutation(addMember, {
    onSuccess: async () => {
      await onChanged()
      setOpen(false)
      setEmail("")
      setRole(AccountRole.MEMBER)
    },
  })
  const roles = isOwner
    ? [{ value: AccountRole.OWNER, label: "Owner" }, ...ROLE_OPTIONS]
    : ROLE_OPTIONS
  function submit(event: FormEvent) {
    event.preventDefault()
    mutation.mutate({ accountSlug, email: email.trim(), role })
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button />}>
        <Plus data-icon="inline-start" /> Add member
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>Add a team member</DialogTitle>
            <DialogDescription>
              They can sign in with this Google email. OpenSight does not send
              an invitation email.
            </DialogDescription>
          </DialogHeader>
          <FieldGroup className="py-5">
            <Field>
              <FieldLabel htmlFor="member-email">Email</FieldLabel>
              <Input
                id="member-email"
                type="email"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                required
                autoFocus
              />
            </Field>
            <Field>
              <FieldLabel>Role</FieldLabel>
              <Select
                value={String(role)}
                onValueChange={(value) => setRole(Number(value) as AccountRole)}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {roles.map((option) => (
                    <SelectItem key={option.value} value={String(option.value)}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            {mutation.isError && (
              <FieldError>
                {errorMessage(mutation.error, "Couldn't add this member.")}
              </FieldError>
            )}
          </FieldGroup>
          <DialogFooter>
            <Button
              type="submit"
              disabled={!email.trim() || mutation.isPending}
            >
              {mutation.isPending ? "Adding…" : "Add member"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function roleLabel(role: number) {
  return (
    (
      { 1: "Owner", 2: "Admin", 3: "Member", 4: "Viewer" } as Record<
        number,
        string
      >
    )[role] ?? "Member"
  )
}
