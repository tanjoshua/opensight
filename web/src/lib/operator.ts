export const COMPED_ACCOUNT_ADMIN_EMAILS = ["jtanjoshua@gmail.com", "liyicheng513@gmail.com"]

export function isCompedAccountAdmin(email: string | undefined) {
  return COMPED_ACCOUNT_ADMIN_EMAILS.includes(email?.trim().toLowerCase() ?? "")
}
