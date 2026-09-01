import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { ApiError, getMe, getSetupStatus, login, logout, claimSetup } from "./api/client";
import type { LoginRequest, MeResponse, SetupClaimRequest } from "./api/types";

export type SessionReady = {
  status: "ready";
  setupOpen: boolean;
  user: MeResponse | null;
};

export type SessionState =
  | { status: "loading" }
  | { status: "error"; message: string }
  | SessionReady;

type SessionContextValue = SessionState & {
  refresh: () => Promise<void>;
  signIn: (body: LoginRequest) => Promise<MeResponse>;
  completeSetup: (body: SetupClaimRequest) => Promise<MeResponse>;
  signOut: () => Promise<void>;
};

const SessionContext = createContext<SessionContextValue | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<SessionState>({ status: "loading" });

  const refresh = useCallback(async () => {
    setState({ status: "loading" });
    try {
      const [setup, user] = await Promise.all([getSetupStatus(), getMe()]);
      setState({ status: "ready", setupOpen: setup.open, user });
    } catch (err) {
      const message =
        err instanceof ApiError
          ? err.message
          : "Unable to reach the control plane.";
      setState({ status: "error", message });
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const signIn = useCallback(async (body: LoginRequest) => {
    const user = await login(body);
    setState({ status: "ready", setupOpen: false, user });
    return user;
  }, []);

  const completeSetup = useCallback(async (body: SetupClaimRequest) => {
    const user = await claimSetup(body);
    setState({ status: "ready", setupOpen: false, user });
    return user;
  }, []);

  const signOut = useCallback(async () => {
    await logout();
    setState({ status: "ready", setupOpen: false, user: null });
  }, []);

  const value = useMemo<SessionContextValue>(
    () => ({
      ...state,
      refresh,
      signIn,
      completeSetup,
      signOut,
    }),
    [completeSetup, refresh, signIn, signOut, state],
  );

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionContextValue {
  const ctx = useContext(SessionContext);
  if (!ctx) {
    throw new Error("useSession must be used inside SessionProvider");
  }
  return ctx;
}
