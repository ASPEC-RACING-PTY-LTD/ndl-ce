export const PVE_TOKEN_FORMAT = "user@realm!tokenid=secret";
export const PVE_TOKEN_EXAMPLE = "root@pam!nodal=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx";

export function pveTokenError(token: string): string | null {
  const value = token.trim();
  if (!value) {
    return `Proxmox API token is required as ${PVE_TOKEN_FORMAT}. Example: ${PVE_TOKEN_EXAMPLE}`;
  }
  const at = value.indexOf("@");
  const bang = value.indexOf("!");
  const eq = value.lastIndexOf("=");
  if (at < 1 || bang < at + 2 || eq < bang + 2 || eq === value.length - 1) {
    if (at < 0 || bang < 0) {
      return `Proxmox API token must be ${PVE_TOKEN_FORMAT}, not the secret alone. Example: ${PVE_TOKEN_EXAMPLE}`;
    }
    return `Proxmox API token must be ${PVE_TOKEN_FORMAT}. Example: ${PVE_TOKEN_EXAMPLE}`;
  }
  const user = value.slice(0, at);
  const realm = value.slice(at + 1, bang);
  const id = value.slice(bang + 1, eq);
  const secret = value.slice(eq + 1);
  if (/\s/.test(user) || /\s|@|!/.test(realm) || /\s/.test(id) || /\s/.test(secret)) {
    return `Proxmox API token must be ${PVE_TOKEN_FORMAT}. Example: ${PVE_TOKEN_EXAMPLE}`;
  }
  return null;
}
