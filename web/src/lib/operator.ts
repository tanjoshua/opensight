export const COMPED_ACCOUNT_ADMIN_EMAIL = "jtanjoshua@gmail.com"

export function isCompedAccountAdmin(email: string | undefined) {
  return email?.trim().toLowerCase() === COMPED_ACCOUNT_ADMIN_EMAIL
}
