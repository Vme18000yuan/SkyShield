/**
 * Authentication-related hooks
 */
import { useQuery } from '@tanstack/react-query'
import { useRouter } from 'next/navigation'
import { useResourceMutation } from '@/hooks/_shared/create-resource-mutation'
import { login, logout, getMe, changePassword, hasAuthTokens } from '@/services/auth.service'
import { buildLoginPath } from '@/lib/auth-runtime.mjs'
import { loadRememberedLogin, saveRememberedLogin } from '@/lib/remembered-login'
import { pushWithRouteProgress } from '@/components/route-progress'
import type { LoginRequest, ChangePasswordRequest, MeResponse } from '@/types/auth.types'

export const authKeys = {
  all: ['auth'] as const,
  me: ['auth', 'me'] as const,
}

/**
 * Get current user information
 */
export function useAuth() {
  const skipAuth = process.env.NEXT_PUBLIC_SKIP_AUTH === 'true'
  const hasToken = !skipAuth && hasAuthTokens()
  const authenticatedForSkipAuth = { authenticated: true } as Awaited<ReturnType<typeof getMe>>
  const unauthenticated = { authenticated: false, user: null } as Awaited<ReturnType<typeof getMe>>

  return useQuery({
    queryKey: authKeys.me,
    queryFn: skipAuth
      ? () => Promise.resolve(authenticatedForSkipAuth)
      : getMe,
    enabled: skipAuth || hasToken,
    initialData: skipAuth
      ? authenticatedForSkipAuth
      : hasToken
        ? undefined
        : unauthenticated,
    staleTime: 1000 * 60 * 5, // Don't re-request within 5 minutes
    retry: false,
  })
}

/**
 * User login
 */
export function useLogin() {
  return useResourceMutation({
    mutationFn: (data: LoginRequest) => login(data),
    loadingToast: {
      key: 'common.status.loading',
      params: {},
      id: 'auth-login',
    },
    onSuccess: ({ toast }) => {
      // Navigation and data prefetch are handled by the login page.
      toast.success('toast.auth.login.success')
    },
    errorFallbackKey: 'auth.loginFailed',
  })
}

/**
 * User logout
 */
export function useLogout() {
  const router = useRouter()

  return useResourceMutation<Awaited<ReturnType<typeof logout>>, void>({
    mutationFn: logout,
    loadingToast: {
      key: 'common.status.loading',
      params: {},
      id: 'auth-logout',
    },
    invalidate: [{ queryKey: authKeys.me }],
    onSuccess: ({ toast }) => {
      toast.success('toast.auth.logout.success')
      pushWithRouteProgress(router, buildLoginPath())
    },
    errorFallbackKey: 'errors.unknown',
  })
}

/**
 * Change password
 */
export function useChangePassword() {
  return useResourceMutation({
    mutationFn: (data: ChangePasswordRequest) => changePassword(data),
    loadingToast: {
      key: 'common.status.updating',
      params: {},
      id: 'change-password',
    },
    onSuccess: ({ toast, variables, queryClient }) => {
      const remembered = loadRememberedLogin()
      const currentUser = queryClient.getQueryData<MeResponse>(authKeys.me)?.user
      const updateFailed = remembered && currentUser?.username === remembered.username &&
        !saveRememberedLogin({ username: remembered.username, password: variables.newPassword })
      toast.success('toast.auth.changePassword.success')
      if (updateFailed) {
        toast.warning('auth.visualLogin.rememberPasswordStorageError', undefined, 'remember-password-storage')
      }
    },
    errorFallbackKey: 'toast.auth.changePassword.error',
  })
}
