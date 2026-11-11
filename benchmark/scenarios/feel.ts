import { Scenario } from '../src/scenario';

const group = 'FEEL expressions';

export const feel: Scenario[] = [
  {
    name: 'feel-numbers', group,
    description: 'Decimal arithmetic, division, power, rounding, floor, modulo, multiply a variable, divide by zero.',
    bpmn: ['feel.bpmn'], process: 'feel_numbers', variables: { amount: 10 }, maxMs: 8000,
  },
  {
    name: 'feel-null-handling', group,
    description: 'A variable that was never set: comparison, if, arithmetic, is defined(), false and null.',
    bpmn: ['feel.bpmn'], process: 'feel_null', maxMs: 8000,
  },
  {
    name: 'feel-strings', group,
    description: 'upper case, substring, string length, concatenation, split, matches, string() of a decimal.',
    bpmn: ['feel.bpmn'], process: 'feel_strings', variables: { name: 'Alice', code: 'AB-123' }, maxMs: 8000,
  },
  {
    name: 'feel-lists', group,
    description: 'for/sum, filters, negative and out-of-range index, count/min/max, distinct values, some, path on a list.',
    bpmn: ['feel.bpmn'], process: 'feel_lists',
    variables: { items: [1, 2, 3], order: { items: [{ sku: 'A', qty: 2 }, { sku: 'B', qty: 1 }, { sku: 'C', qty: 5 }] } },
    maxMs: 8000,
  },
  {
    name: 'feel-dates', group,
    description: 'Date arithmetic across month ends and a leap year, durations, comparison, a date stored without string().',
    bpmn: ['feel.bpmn'], process: 'feel_dates', maxMs: 8000,
  },
  {
    name: 'feel-contexts', group,
    description: 'Context literals, a context referring to itself, paths into variables, a missing path, get value, context put.',
    bpmn: ['feel.bpmn'], process: 'feel_context',
    variables: { order: { id: 'O-9', customer: { name: 'Ada' } } }, maxMs: 8000,
  },
];
