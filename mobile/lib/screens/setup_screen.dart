import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../theme.dart';
import '../widgets/logo.dart';

/// What the app shows when the server has nobody who can approve anyone.
///
/// This is not a page to be sent to. A server in this state cannot do anything
/// for anyone - every account is waiting on an approval that no one can give -
/// so the app says so where the person already is, and offers the way out.
///
/// Whoever takes it, takes it: the server decides that under a lock and refuses
/// the second caller. The confirmation exists so nobody claims it by accident,
/// not because the app is the thing enforcing it.
class SetupScreen extends StatefulWidget {
  const SetupScreen({super.key});
  @override
  State<SetupScreen> createState() => _SetupScreenState();
}

class _SetupScreenState extends State<SetupScreen> {
  bool _claiming = false;
  String? _error;

  Future<void> _claim() async {
    setState(() {
      _claiming = true;
      _error = null;
    });
    final err = await context.read<AppState>().claimAdmin();
    if (!mounted) return;
    setState(() {
      _claiming = false;
      _error = err;
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: T.bg,
      body: Center(
        child: SingleChildScrollView(
          padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 40),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 420),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const DrossLogo(size: 84, showWordmark: false),
                const SizedBox(height: 22),
                const Text('No administrator yet',
                    style: TextStyle(
                        fontSize: 24,
                        fontWeight: FontWeight.w600,
                        letterSpacing: -0.4,
                        color: T.text),
                    textAlign: TextAlign.center),
                const SizedBox(height: 10),
                const Text(
                  'Nobody can approve accounts on this server, including yours. '
                  'Claim it and you become the first admin.',
                  style: T.metaFaint,
                  textAlign: TextAlign.center,
                ),
                const SizedBox(height: 26),
                if (_error != null) ...[
                  Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 14, vertical: 11),
                    decoration: BoxDecoration(
                      color: const Color(0xFF2A1119),
                      border: Border.all(color: const Color(0xFF6B2338)),
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: Text(_error!,
                        style: T.metaFaint.copyWith(color: T.danger)),
                  ),
                  const SizedBox(height: 16),
                ],
                SizedBox(
                  width: double.infinity,
                  child: Material(
                    color: T.pill,
                    borderRadius: BorderRadius.circular(T.rPill),
                    child: InkWell(
                      borderRadius: BorderRadius.circular(T.rPill),
                      onTap: _claiming ? null : _claim,
                      child: Padding(
                        padding: const EdgeInsets.symmetric(vertical: 15),
                        child: Center(
                          child: _claiming
                              ? const SizedBox(
                                  width: 18,
                                  height: 18,
                                  child: CircularProgressIndicator(
                                      strokeWidth: 2, color: T.pillText))
                              : const Text('Make me the administrator',
                                  style: TextStyle(
                                      color: T.pillText,
                                      fontSize: 15,
                                      fontWeight: FontWeight.w600)),
                        ),
                      ),
                    ),
                  ),
                ),
                const SizedBox(height: 18),
                const Text(
                  'Only offered while there is no admin. Whoever claims it first '
                  'gets it, and everyone else waits to be approved.',
                  style: T.metaFaint,
                  textAlign: TextAlign.center,
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
