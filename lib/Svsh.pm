package Svsh;

# ABSTRACT: Process supervision shell for Perp and S6

our $VERSION = "1.000000";
$VERSION = eval $VERSION;

use Moo::Role;

has 'basedir' => (
	is => 'ro',
	required => 1
);

requires qw/status start stop enable disable fg/;

sub run_cmd {
	my ($self, $cmd, @args) = @_;

	print "Running command: ", join(' ', $cmd, @args), "\n";

	system($cmd, @args);
}

1;
__END__
