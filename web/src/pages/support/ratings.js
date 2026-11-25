// The 1 to 5 rating scale shared by the ticket page and the email survey
// page. The server accepts 1 to 5 (SubmitCSAT, SubmitSurvey).
export const RATINGS = [
  { value: 1, label: 'Bad' },
  { value: 2, label: 'Poor' },
  { value: 3, label: 'Okay' },
  { value: 4, label: 'Good' },
  { value: 5, label: 'Great' },
];

// Matches the comment cap on the server (support_survey.go, SubmitCSAT).
export const RATING_COMMENT_MAX = 2000;
