import { useEffect } from 'preact/hooks';
import Router from 'preact-router';
import { authLoading, isLoggedIn, loadUser, appMeta } from './state/auth';
import { Layout } from './components/layout';
import { LoginPage } from './pages/auth/login';
import { RegisterPage } from './pages/auth/register';
import { VerifyEmailPage } from './pages/auth/verify-email';
import { VerifyEmailSentPage } from './pages/auth/verify-email-sent';
import { SSOCallbackPage } from './pages/auth/sso-callback';
import MFAChallenge from './pages/auth/mfa-challenge';
import { ForgotPasswordPage } from './pages/auth/forgot-password';
import { ResetPasswordPage } from './pages/auth/reset-password';
import { AccountRecoverPage } from './pages/account/recover';
import { ConfirmDeletePage } from './pages/account/confirm-delete';
import { ConfirmRecoverPage } from './pages/account/confirm-recover';
import { DashboardPage } from './pages/dashboard';
import { MonitorListPage } from './pages/monitors/list';
import { MonitorDetailPage } from './pages/monitors/detail';
import { AlertListPage } from './pages/alerts/list';
import { AlertDetailPage } from './pages/alerts/detail';
import { OnCallPage } from './pages/oncall';
import { SettingsPage } from './pages/settings';
import { ServiceListPage } from './pages/services/list';
import { ServiceDetailPage } from './pages/services/detail';
import { IncidentListPage } from './pages/incidents/list';
import { IncidentDetailPage } from './pages/incidents/detail';
import { SupportListPage } from './pages/support/list';
import { SupportDetailPage } from './pages/support/detail';

function AuthGuard({ children }) {
  if (authLoading.value) {
    return <div class="loading-screen"><div class="spinner" /><p>Loading...</p></div>;
  }
  if (!isLoggedIn.value) {
    if (typeof window !== 'undefined') window.location.href = '/login';
    return null;
  }
  return <Layout>{children}</Layout>;
}

function handleRoute(e) {
  // Scroll to top on route change.
  if (typeof window !== 'undefined') window.scrollTo(0, 0);
}

export function App() {
  useEffect(() => {
    loadUser();
  }, []);

  return (
    <Router onChange={handleRoute}>
      <LoginPage path="/login" />
      <RegisterPage path="/register" />
      <VerifyEmailPage path="/verify-email" />
      <VerifyEmailSentPage path="/verify-email-sent" />
      <SSOCallbackPage path="/auth/sso-callback" />
      <MFAChallenge path="/auth/mfa-challenge" />
      <ForgotPasswordPage path="/forgot-password" />
      <ResetPasswordPage path="/reset-password" />
      <AccountRecoverPage path="/account/recover" />
      <ConfirmDeletePage path="/account/confirm-delete" />
      <ConfirmRecoverPage path="/account/confirm-recover" />
      <AuthRoute path="/" component={DashboardPage} />
      <AuthRoute path="/monitors" component={MonitorListPage} />
      <AuthRoute path="/monitors/:id" component={MonitorDetailPage} />
      <AuthRoute path="/alerts" component={AlertListPage} />
      <AuthRoute path="/alerts/:id" component={AlertDetailPage} />
      {appMeta.value?.edition !== 'foss' && <AuthRoute path="/oncall" component={OnCallPage} />}
      <AuthRoute path="/settings" component={SettingsPage} />
      <AuthRoute path="/settings/:tab" component={SettingsPage} />
      {appMeta.value?.edition !== 'foss' && <AuthRoute path="/services" component={ServiceListPage} />}
      {appMeta.value?.edition !== 'foss' && <AuthRoute path="/services/:id" component={ServiceDetailPage} />}
      <AuthRoute path="/incidents" component={IncidentListPage} />
      <AuthRoute path="/incidents/:id" component={IncidentDetailPage} />
      <AuthRoute path="/support" component={SupportListPage} />
      <AuthRoute path="/support/:id" component={SupportDetailPage} />
    </Router>
  );
}

function AuthRoute({ component: Component, ...props }) {
  return (
    <AuthGuard>
      <Component {...props} />
    </AuthGuard>
  );
}
